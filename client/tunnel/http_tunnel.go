// Package tunnel implements the HTTP tunnel for fgrok.
//
// Provides functionality for:
// - Forwarding HTTP requests between client and server
// - Maintaining persistent connection via gRPC stream
// - Properly processing HTTP headers
package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"github.com/flrossetto/fgrok/internal/proto"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// HTTPTunnel implements the HTTPTunnel traffic tunnel.
type HTTPTunnel struct {
	logger *logrus.Entry
	tunnel config.TunnelConfig
	conn   *grpc.ClientConn
}

// NewHTTPTunnel creates a new HTTP tunnel instance.
//
// Parameters:
//   - logger: Logger for event recording
//   - tunnel: Tunnel configuration
//   - conn: gRPC connection to server
//
// Returns:
//   - *TunnelHTTP: NewHTTPTunnel HTTP tunnel instance
func NewHTTPTunnel(logger *logrus.Entry, tunnel config.TunnelConfig, conn *grpc.ClientConn) *HTTPTunnel {
	return &HTTPTunnel{
		logger: logger,
		tunnel: tunnel,
		conn:   conn,
	}
}

// Start initiates the HTTP tunnel.
//
// Parameters:
//   - ctx: Context for cancellation control
//
// Returns:
//   - error: Error if connection fails
//
// The method:
// - Establishes gRPC stream with server
// - Processes received messages in separate goroutines
// - Automatically reconnects on failure
func (t *HTTPTunnel) Start(ctx context.Context) error {
	ctx = metadata.AppendToOutgoingContext(ctx, config.MetadataSubdomainKey, t.tunnel.Subdomain)

	t.logger.Infof("Starting HTTP tunnel for subdomain: %s", t.tunnel.Subdomain)

	stream, err := proto.NewHttpTunnelServiceClient(t.conn).Stream(ctx)
	if err != nil {
		t.logger.WithError(err).Error("Failed to establish HTTP tunnel stream")

		return fgrokerr.New(
			fgrokerr.CodeConnectionError,
			"failed to establish HTTP tunnel stream",
			fgrokerr.WithError(err),
		)
	}

	for {
		msg, err := stream.Recv()
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
				t.logger.WithFields(logrus.Fields{
					"subdomain": t.tunnel.Subdomain,
				}).Warn("Subdomain already in use")

				return nil
			}

			t.logger.WithFields(logrus.Fields{
				"subdomain": t.tunnel.Subdomain,
				"error":     err,
			}).Error("Failed to receive message from HTTP tunnel stream")

			return fgrokerr.New(
				fgrokerr.CodeTunnelError,
				"failed to receive message from HTTP tunnel stream",
				fgrokerr.WithError(err),
			)
		}

		// Log opcional: você pode logar que uma nova mensagem foi recebida, dependendo do volume.
		t.logger.WithField("subdomain", t.tunnel.Subdomain).Debug("Received message from server")

		go t.processMessage(ctx, stream, msg)
	}
}

// processMessage handles an HTTP message received from server.
//
// Parameters:
//   - ctx: Context for cancellation control
//   - stream: gRPC stream to send responses
//   - msg: Message received from server
//
// The method:
// - Parses received HTTP request
// - Forwards to local service
// - Sends response back to server
// - Safely handles headers (removes hop-by-hop)
func (t *HTTPTunnel) processMessage(ctx context.Context, stream proto.HttpTunnelService_StreamClient, msg *proto.HttpUpstream) {
	log := t.logger.WithField("requestId", msg.GetRequestId())

	// Parse the HTTP request from the received msg
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(msg.GetPayload())))
	if err != nil {
		log.WithError(err).Error("Failed to parse HTTP request")

		return
	}

	defer func() {
		if err := req.Body.Close(); err != nil {
			t.logger.WithError(err).Warn("Failed to close request body")
		}
	}()

	// Build target URL for local service
	targetURL := &url.URL{
		Scheme: "http",
		Host:   t.tunnel.LocalAddr,
		Path:   req.URL.Path,
	}
	targetURL.RawQuery = req.URL.RawQuery

	// Create new request to local service
	newReq, err := http.NewRequestWithContext(
		ctx,
		req.Method,
		targetURL.String(),
		req.Body,
	)
	if err != nil {
		log.WithError(err).Error("Failed to create local request")

		return
	}

	// Copy headers, removing hop-by-hop headers
	newReq.Header = cloneHeaderSansHopByHop(req.Header)
	newReq.Host = t.tunnel.LocalAddr

	// Add forwarding headers
	appendForwardHeaders(newReq.Header, req)

	// Execute request to local service
	resp, err := http.DefaultClient.Do(newReq)
	if err != nil {
		log.WithError(err).Error("Failed to execute local request")

		return
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.logger.WithError(err).Warn("Failed to close response body")
		}
	}()

	// Prepare response to send back to server
	var respBuf bytes.Buffer
	if err := resp.Write(&respBuf); err != nil {
		log.WithError(err).Error("Failed to serialize response")

		return
	}

	// Send response back via gRPC stream
	response := &proto.HttpDownstream{
		RequestId: msg.GetRequestId(),
		Payload:   respBuf.Bytes(),
	}

	if err := stream.Send(response); err != nil {
		log.WithError(err).Error("Failed to send response to server")

		return
	}

	log.WithFields(logrus.Fields{
		"host":   req.Host,
		"path":   req.URL.Path,
		"status": resp.StatusCode,
	}).Info("Processed request")
}

// cloneHeaderSansHopByHop clones HTTP headers while removing hop-by-hop headers.
//
// Parameters:
//   - h: Original HTTP headers
//
// Returns:
//   - http.Header: New headers without hop-by-hop headers
func cloneHeaderSansHopByHop(h http.Header) http.Header {
	hopHeaders := map[string]struct{}{
		"Connection":          {},
		"Proxy-Connection":    {},
		"Keep-Alive":          {},
		"Proxy-Authenticate":  {},
		"Proxy-Authorization": {},
		"TE":                  {},
		"Trailer":             {},
		"Transfer-Encoding":   {},
		"Upgrade":             {},
	}

	dst := make(http.Header)

	for k, vv := range h {
		if _, isHop := hopHeaders[k]; isHop {
			continue
		}

		for _, v := range vv {
			dst.Add(k, v)
		}
	}

	return dst
}

// appendForwardHeaders adds standard forwarding headers to HTTP request.
//
// Parameters:
//   - h: Headers to modify
//   - req: Original HTTP request
//
// Adds:
// - X-Forwarded-For
// - X-Forwarded-Host
// - X-Forwarded-Proto
func appendForwardHeaders(h http.Header, req *http.Request) {
	// X-Forwarded-For
	if ip, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		if prior := h.Get("X-Forwarded-For"); prior != "" {
			h.Set("X-Forwarded-For", prior+", "+ip)
		} else {
			h.Set("X-Forwarded-For", ip)
		}
	}

	// X-Forwarded-Host
	if req.Host != "" {
		h.Set("X-Forwarded-Host", req.Host)
	}

	// X-Forwarded-Proto
	proto := "http"
	if strings.EqualFold(h.Get("X-Forwarded-Proto"), "https") {
		proto = "https"
	}

	h.Set("X-Forwarded-Proto", proto)
}
