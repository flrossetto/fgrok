package handler

import (
	"net/http"
)

// HTTPHandler implements a simple HTTP handler that redirects all requests to HTTPS
// using HTTP 308 (Permanent Redirect) status code.
type HTTPHandler struct{}

// NewHTTPHandler creates a new instance of HTTPHandler.
// Returns:
// - *HTTPHandler: ready-to-use HTTP handler instance
func NewHTTPHandler() *HTTPHandler {
	return &HTTPHandler{}
}

// ServeHTTP handles incoming HTTP requests by redirecting them to HTTPS.
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
// Behavior:
// - Constructs HTTPS URL from original request
// - Responds with HTTP 308 (Permanent Redirect)
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u := *r.URL
	u.Scheme = "https"
	u.Host = r.Host
	http.Redirect(w, r, u.String(), http.StatusPermanentRedirect) // 308
}
