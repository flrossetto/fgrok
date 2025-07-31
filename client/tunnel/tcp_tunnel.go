// Package tunnel implements the TCP tunnel for fgrok.
//
// Provides functionality for:
// - Forwarding TCP traffic between client and server
// - Managing multiple concurrent TCP connections
// - Maintaining connection state via gRPC stream
package tunnel

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"github.com/flrossetto/fgrok/internal/proto"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TCPTunnel implements the TCPTunnel traffic tunnel.
type TCPTunnel struct {
	logger     *logrus.Entry
	tunnel     config.TunnelConfig
	conn       *grpc.ClientConn
	connection sync.Map
}

// NewTCPTunnel creates a new TCP tunnel instance.
//
// Parameters:
//   - logger: Logger for event recording
//   - tunnel: Tunnel configuration
//   - conn: gRPC connection to server
//
// Returns:
//   - *TunnelTCP: New TCP tunnel instance
func NewTCPTunnel(logger *logrus.Entry, tunnel config.TunnelConfig, conn *grpc.ClientConn) *TCPTunnel {
	return &TCPTunnel{
		logger: logger,
		tunnel: tunnel,
		conn:   conn,
	}
}

// Start initiates the TCP tunnel.
//
// Parameters:
//   - ctx: Context for cancellation control
//
// Returns:
//   - error: Error if connection fails
//
// The method:
// - Establishes gRPC stream with server
// - Processes received messages
// - Automatically reconnects on failure
func (t *TCPTunnel) Start(ctx context.Context) error {
	ctx = metadata.AppendToOutgoingContext(ctx, config.MetadataRemoteAddrKey, t.tunnel.RemoteAddr)

	stream, err := proto.NewTcpTunnelServiceClient(t.conn).Stream(ctx)
	if err != nil {
		return fgrokerr.New(fgrokerr.CodeConnectionError, "failed to establish TCP tunnel stream", fgrokerr.WithError(err))
	}

	for {
		msg, err := stream.Recv()
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
				t.logger.WithFields(logrus.Fields{
					"remoteAddr": t.tunnel.RemoteAddr,
				}).Warn("Remote address already in use ")

				return nil
			}

			t.logger.WithFields(logrus.Fields{
				"remoteAddr": t.tunnel.RemoteAddr,
				"error":      err,
			}).Error("Failed to receive message from TCP tunnel stream — retrying in 1s")

			time.Sleep(time.Second)

			continue
		}

		t.processMessage(stream, msg)
	}
}

// processMessage handles a TCP message received from server.
//
// Parameters:
//   - ctx: Context for cancellation control
//   - stream: gRPC stream to send responses
//   - msg: Message received from server
//
// The method:
// - Manages TCP connection lifecycle
// - Handles both new and existing connections
// - Forwards data bidirectionally
func (t *TCPTunnel) processMessage(stream proto.TcpTunnelService_StreamClient, msg *proto.TcpUpstream) {
	log := t.logger.WithField("connectionId", msg.GetConnectionId())

	conn, loaded := t.connection.LoadOrStore(msg.GetConnectionId(), &net.TCPConn{})

	tcpConn, _ := conn.(*net.TCPConn)

	if !loaded {
		addr, err := net.ResolveTCPAddr("tcp", t.tunnel.LocalAddr)
		if err != nil {
			log.WithError(err).Error("Failed to resolve TCP address")

			return
		}

		tcpConn, err = net.DialTCP("tcp", nil, addr)
		if err != nil {
			log.WithFields(logrus.Fields{
				"addr": t.tunnel.LocalAddr,
			}).WithError(err).Error("Failed to establish TCP connection")

			return
		}

		log.WithFields(logrus.Fields{
			"localAddr":  t.tunnel.LocalAddr,
			"remoteAddr": t.tunnel.RemoteAddr,
		}).Info("Established new TCP tunnel connection")
		t.connection.Store(msg.GetConnectionId(), tcpConn)

		// Start goroutine to read from TCP connection
		go (func() {
			buf := make([]byte, config.BufferSize)
			for {
				n, err := tcpConn.Read(buf)
				if err != nil {
					log.WithFields(logrus.Fields{
						"operation": "read",
						"connID":    msg.GetConnectionId(),
					}).WithError(err).Error("TCP connection read failed")

					return
				}
				log.WithFields(logrus.Fields{
					"bytes":     n,
					"operation": "read",
					"connID":    msg.GetConnectionId(),
				}).Debug("Successfully read from TCP connection")

				// Send read data via gRPC stream
				err = stream.Send(&proto.TcpDownstream{
					ConnectionId: msg.GetConnectionId(),
					Payload:      buf[:n],
				})
				if err != nil {
					log.WithError(err).Warn("Failed to send TCP response")
				}
			}
		})()
	}

	// Write data to connection if payload exists
	if len(msg.GetPayload()) > 0 {
		_, err := tcpConn.Write(msg.GetPayload())
		if err != nil {
			log.WithFields(logrus.Fields{
				"operation": "write",
			}).WithError(err).Error("Failed to write to TCP connection")

			return
		}

		log.WithFields(logrus.Fields{
			"operation": "write",
		}).Debug("Successfully wrote data to TCP connection")
	}
}
