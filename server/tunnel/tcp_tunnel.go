package tunnel

import (
	"context"
	"net"
	"sync"

	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"github.com/flrossetto/fgrok/internal/firsterr"
	"github.com/flrossetto/fgrok/internal/mediator"
	"github.com/flrossetto/fgrok/internal/proto"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	// ErrConnectionFailed indicates TCP connection establishment failure
	ErrConnectionFailed = fgrokerr.New(fgrokerr.CodeConnectionError, "TCP connection failed")
	// ErrInvalidAddress indicates malformed or empty address
	ErrInvalidAddress = fgrokerr.New(fgrokerr.CodeHostError, "invalid TCP address")
)

// TCPTunnel implements gRPC service for TCPTunnel tunneling
//
// Responsibilities:
// - Bidirectional TCPTunnel data streaming
// - Connection lifecycle management
// - Remote address validation
//
// Thread-safety:
// - Safe for concurrent use (one stream per connection)
// - Uses sync.Map for thread-safe connection tracking
type TCPTunnel struct {
	proto.UnimplementedTcpTunnelServiceServer

	logger      *logrus.Logger
	mediator    mediator.Mediator
	connections sync.Map
}

// NewTCPTunnel creates a new TCP tunnel service instance
//
// Parameters:
// - logger: Logger for service operations
// - mediator: Mediator for connection data routing
//
// Returns:
// - *TCPTunnelService: Configured service instance
func NewTCPTunnel(logger *logrus.Logger, mediator mediator.Mediator) *TCPTunnel {
	return &TCPTunnel{
		logger:   logger,
		mediator: mediator,
	}
}

// Stream establishes a bidirectional TCP tunnel stream
//
// Flow:
// 1. Validates remote address from metadata
// 2. Creates TCP listener for incoming connections
// 3. Processes incoming data streams
// 4. Returns on stream error or completion
//
// Parameters:
// - stream: Bidirectional gRPC stream
//
// Returns:
// - error: Stream error or nil on normal termination
func (s *TCPTunnel) Stream(stream proto.TcpTunnelService_StreamServer) error {
	remoteAddr, err := s.validateStreamMetadata(stream)
	if err != nil {
		return err
	}

	log := s.logger.WithFields(logrus.Fields{
		"remoteAddr": remoteAddr,
		"component":  "tcp_tunnel",
	})
	log.Info("New TCP tunnel stream initiated")

	if s.connectionExists(remoteAddr) {
		err := fgrokerr.New(fgrokerr.CodeConnectionError, "tunnel already exists for this remote address")

		log.Error("Failed to register tunnel")

		return fgrokerr.ToGRPC(err)
	}

	listener, err := s.createTCPListener(stream.Context(), remoteAddr, log)
	if err != nil {
		return err
	}

	defer func() {
		s.connections.Delete(remoteAddr)

		_ = listener.Close()
	}()

	log.Info("TCP listener started successfully")

	_, g := firsterr.WithContext(stream.Context())
	defer g.Cancel()

	g.Go(func(context.Context) error {
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.WithError(err).Error("Failed to accept TCP connection")

				return fgrokerr.New(fgrokerr.CodeConnectionError, "failed to accept TCP connection", fgrokerr.WithError(err))
			}

			connectionID := uuid.New().String()
			log.WithFields(logrus.Fields{
				"connectionID": connectionID,
				"remoteAddr":   conn.RemoteAddr().String(),
			}).Info("Accepted new TCP tunnel connection")

			go s.handleConnection(stream, connectionID, conn)
		}
	})

	g.Go(func(context.Context) error {
		for {
			msg, err := stream.Recv()
			if err != nil {
				log.WithError(err).Error("Stream receive error")

				return fgrokerr.New(fgrokerr.CodeConnectionError, "stream receive error", fgrokerr.WithError(err))
			}

			if err := s.mediator.Send(msg.GetConnectionId(), msg); err != nil {
				return err
			}
		}
	})

	err = g.Wait()
	if err != nil {
		return fgrokerr.ToGRPC(err)
	}

	return nil
}

func (s *TCPTunnel) validateStreamMetadata(stream proto.TcpTunnelService_StreamServer) (string, error) {
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		err := status.Error(codes.Unauthenticated, "missing metadata")
		s.logger.WithError(err).Error("Failed to get metadata from context")

		return "", fgrokerr.New(fgrokerr.CodeInvalidInput, "missing metadata", fgrokerr.WithError(err))
	}

	remoteAddr := getMetadataValue(md, config.MetadataRemoteAddrKey)
	if remoteAddr == "" {
		err := fgrokerr.New(fgrokerr.CodeHostError, "remote address cannot be empty")
		s.logger.WithError(err).Error("Invalid remote address")

		return "", err
	}

	return remoteAddr, nil
}

func (s *TCPTunnel) connectionExists(remoteAddr string) bool {
	var exists bool

	s.connections.Range(func(key, _ any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}

		if k == remoteAddr {
			exists = true

			return false
		}

		return true
	})

	return exists
}

func (s *TCPTunnel) createTCPListener(ctx context.Context, remoteAddr string, log *logrus.Entry) (net.Listener, error) {
	var lc net.ListenConfig

	listener, err := lc.Listen(ctx, "tcp", remoteAddr)
	if err != nil {
		log.Error("Failed to start TCP listener")

		return nil, fgrokerr.New(fgrokerr.CodeConnectionError, "failed to start TCP listener", fgrokerr.WithError(err))
	}

	s.connections.Store(remoteAddr, listener)

	return listener, nil
}

func (s *TCPTunnel) handleConnection(stream proto.TcpTunnelService_StreamServer, connectionID string, conn net.Conn) {
	unregister, _ := s.mediator.RegisterHandler(func(correlationID string, msg *proto.TcpDownstream) {
		if correlationID == connectionID {
			if _, err := conn.Write(msg.GetPayload()); err != nil {
				s.logger.WithError(err).WithField("connectionID", connectionID).Warn("Failed to write to TCP connection")
			}
		}
	})

	defer func() {
		unregister()

		_ = conn.Close()

		s.connections.Delete(connectionID)

		s.logger.WithFields(logrus.Fields{
			"connectionID": connectionID,
			"remoteAddr":   conn.RemoteAddr().String(),
		}).Info("TCP connection closed")
	}()

	buf := make([]byte, config.BufferSize)

	for {
		n, err := conn.Read(buf)
		if err != nil {
			s.logger.WithFields(logrus.Fields{
				"connectionID": connectionID,
				"operation":    "read",
			}).WithError(err).Error("TCP connection read failed")

			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		err = stream.Send(&proto.TcpUpstream{
			ConnectionId: connectionID,
			Payload:      data[:n],
		})
		if err != nil {
			s.logger.WithError(err).WithField("connectionID", connectionID).Error("Failed to send TCP tunnel request")

			return
		}

		s.logger.WithFields(logrus.Fields{
			"connectionID": connectionID,
			"bytesSent":    n,
		}).Debug("TCP data sent successfully")
	}
}
