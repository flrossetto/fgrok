// Package handler provides HTTP request handlers for the fgrok server.
// Includes handlers for:
// - HTTP to HTTPS redirection
// - HTTPS request processing and tunneling
package handler

import (
	"context"
	"io"
	"maps"
	"net/http"
	"sync"

	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/internal/mediator"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// HTTPSHandler implements an HTTP handler for HTTPS requests that:
// - Forwards requests to clients via mediator
// - Manages response correlation using UUIDs
// - Handles timeouts and error cases
// Fields:
// - logger: Logger for request/response logging
// - mediator: Message mediator for client communication
// - unregister: Function to unregister from mediator
// - responseCh: Map for tracking pending responses
type HTTPSHandler struct {
	logger     *logrus.Logger
	mediator   mediator.Mediator
	unregister func()
	responseCh sync.Map
}

// NewHTTPSHandler creates a new HTTPS handler instance.
// Parameters:
// - logger: Configured logger
// - mediator: Mediator for client communication
// Returns:
// - *HTTPSHandler: Configured handler instance
// - error: If registration with mediator fails
func NewHTTPSHandler(logger *logrus.Logger, mediator mediator.Mediator) (*HTTPSHandler, error) {
	h := &HTTPSHandler{
		logger:   logger,
		mediator: mediator,
	}

	responseHandler := func(correlationID string, resp *http.Response) error {
		if value, loaded := h.responseCh.LoadAndDelete(correlationID); loaded {
			ch, _ := value.(chan *http.Response)
			ch <- resp

			close(ch)
		}

		return nil
	}

	var err error

	h.unregister, err = mediator.RegisterHandler(responseHandler)
	if err != nil {
		return nil, err
	}

	return h, nil
}

// ServeHTTP handles incoming HTTPS requests by:
// 1. Creating a correlation ID for the request
// 2. Forwarding request to clients via mediator
// 3. Waiting for response with timeout
// 4. Writing response back to client
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (h *HTTPSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), config.DefaultRequestTimeout)
	defer cancel()

	correlationID := uuid.NewString()

	respCh := make(chan *http.Response, 1)
	h.responseCh.Store(correlationID, respCh)

	h.mediator.AsyncSend(correlationID, r)

	select {
	case resp := <-respCh:
		defer func() {
			if err := resp.Body.Close(); err != nil {
				h.logger.WithError(err).Warn("Failed to close response body")
			}
		}()

		maps.Copy(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)

		_, err := io.Copy(w, resp.Body)
		if err != nil {
			h.logger.WithError(err).Error("Failed to write response")
		}

	case <-ctx.Done():
		if value, loaded := h.responseCh.LoadAndDelete(correlationID); loaded {
			ch, _ := value.(chan *http.Response)
			close(ch)
		}

		w.WriteHeader(http.StatusGatewayTimeout)
	}
}

// Shutdown performs cleanup by unregistering from mediator.
// Should be called when handler is no longer needed.
func (h *HTTPSHandler) Shutdown() {
	h.unregister()
}
