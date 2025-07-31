// Package tunnel implements HTTP and TCP tunneling functionality for fgrok.
//
// Provides:
// - HTTP tunnel implementation for proxying HTTP requests
// - Subdomain validation and routing
// - Bidirectional request/response streaming
// - Error handling and logging
package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"github.com/flrossetto/fgrok/internal/mediator"
	"github.com/flrossetto/fgrok/internal/proto"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/metadata"
)

var (
	// ErrInvalidHost indicates the host format is invalid (less than 3 parts)
	ErrInvalidHost = fgrokerr.New(fgrokerr.CodeInvalidInput, "invalid host format")
	// ErrSubdomainMismatch indicates the request subdomain doesn't match expected
	ErrSubdomainMismatch = fgrokerr.New(fgrokerr.CodeInvalidInput, "subdomain mismatch")
)

// HTTPTunnel implements gRPC service for HTTPTunnel tunneling
//
// Responsibilities:
// - Bidirectional HTTPTunnel request/response streaming
// - Subdomain validation and routing
// - Request/response mediation
// - Error handling and logging
//
// Thread-safety:
// - Safe for concurrent use (one stream per connection)
type HTTPTunnel struct {
	proto.UnimplementedHttpTunnelServiceServer

	logger   *logrus.Logger    // Logger for service operations
	mediator mediator.Mediator // Mediator for request routing
}

// NewHTTPTunnel creates a new HTTP tunnel service instance
//
// Parameters:
// - logger: Configured logger for service operations
// - mediator: Mediator instance for request routing
//
// Returns:
// - *HTTPTunnelService: Configured service instance ready for use
func NewHTTPTunnel(logger *logrus.Logger, mediator mediator.Mediator) *HTTPTunnel {
	return &HTTPTunnel{
		logger:   logger,
		mediator: mediator,
	}
}

// Stream establishes a bidirectional HTTP tunnel stream
//
// Flow:
// 1. Extracts subdomain from metadata
// 2. Registers request handler with mediator
// 3. Processes incoming responses
// 4. Returns on stream error or completion
//
// Parameters:
// - stream: Bidirectional gRPC stream for HTTP tunneling
//
// Returns:
// - error: Stream error or nil on normal termination
func (s *HTTPTunnel) Stream(stream proto.HttpTunnelService_StreamServer) error {
	subdomain, err := s.getSubdomain(stream.Context())
	if err != nil {
		return err
	}

	log := s.logger.WithFields(logrus.Fields{
		"subdomain": subdomain,
		"component": "http_tunnel",
	})

	unregister, err := s.registerRequestHandler(stream, subdomain)
	if err != nil {
		return fgrokerr.ToGRPC(err)
	}

	defer unregister()

	log.Info("New HTTP tunnel stream established")

	for {
		msg, err := stream.Recv()
		if err != nil {
			log.WithError(err).Error("Stream receive error")

			return fgrokerr.New(fgrokerr.CodeConnectionError, "failed to receive stream message", fgrokerr.WithError(err))
		}

		if err := s.processResponse(msg); err != nil {
			log.WithError(err).WithField("requestId", msg.GetRequestId()).Error("Failed to process response")
		}
	}
}

// getSubdomain extracts and validates subdomain from stream metadata
//
// Parameters:
// - ctx: Context containing gRPC metadata
//
// Returns:
// - string: Extracted subdomain
// - error: If metadata is missing or invalid
func (s *HTTPTunnel) getSubdomain(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", fgrokerr.New(fgrokerr.CodeInvalidInput, "missing metadata")
	}

	return getMetadataValue(md, config.MetadataSubdomainKey), nil
}

// registerRequestHandler registers mediator handler for incoming HTTP requests
//
// Parameters:
// - stream: gRPC stream for sending responses
// - subdomain: Expected subdomain for validation
//
// Behavior:
// - Validates subdomain matches expected
// - Serializes valid requests to protobuf
// - Sends requests via gRPC stream
func (s *HTTPTunnel) registerRequestHandler(stream proto.HttpTunnelService_StreamServer, subdomain string) (func(), error) {
	unregister, err := s.mediator.RegisterUniqueHandler(func(correlationID string, req *http.Request) {
		payload, err := s.processRequest(subdomain, req)
		if err != nil {
			if !fgrokerr.Is(err, ErrSubdomainMismatch) {
				s.logger.WithError(err).Error("Request processing failed")
			}

			return
		}

		err = stream.Send(&proto.HttpUpstream{
			RequestId: correlationID,
			Payload:   payload,
		})
		if err != nil {
			s.logger.WithError(err).Error("Failed to send response")
		}
	})
	if err != nil {
		return nil, err
	}

	return unregister, nil
}

// processRequest validates and serializes incoming HTTP requests
//
// Parameters:
// - subdomain: Expected subdomain for validation
// - req: HTTP request to process
//
// Returns:
// - []byte: Serialized request payload
// - error: If validation or serialization fails
func (s *HTTPTunnel) processRequest(subdomain string, req *http.Request) ([]byte, error) {
	if err := validateSubdomain(req.Host, subdomain); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := req.Write(&buf); err != nil {
		return nil, fgrokerr.New(fgrokerr.CodeSerializationError, "failed to serialize request", fgrokerr.WithError(err))
	}

	return buf.Bytes(), nil
}

// processResponse handles and routes incoming HTTP responses
//
// Parameters:
// - msg: gRPC message containing HTTP response
//
// Returns:
// - error: If response parsing fails
//
// Behavior:
// - Parses HTTP response from protobuf
// - Routes response to appropriate handler via mediator
func (s *HTTPTunnel) processResponse(msg *proto.HttpDownstream) error {
	//nolint:bodyclose
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(msg.GetPayload())), nil)
	if err != nil {
		s.mediator.AsyncSend(msg.GetRequestId(), &http.Response{})

		return fgrokerr.New(fgrokerr.CodeSerializationError, "failed to parse response", fgrokerr.WithError(err))
	}

	s.mediator.AsyncSend(msg.GetRequestId(), resp)

	return nil
}

// validateSubdomain validates host matches expected subdomain format
//
// Parameters:
// - host: Full hostname from request
// - expected: Expected subdomain prefix
//
// Returns:
// - error: If host format is invalid or subdomain doesn't match
func validateSubdomain(host, expected string) error {
	parts := strings.Split(host, ".")
	if len(parts) < config.MinHTTPParts {
		return ErrInvalidHost
	}

	if parts[0] != expected {
		return ErrSubdomainMismatch
	}

	return nil
}

// getMetadataValue safely extracts first value for metadata key
//
// Parameters:
// - md: gRPC metadata
// - key: Metadata key to extract
//
// Returns:
// - string: First value for key, or empty string if not found
func getMetadataValue(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}

	return values[0]
}
