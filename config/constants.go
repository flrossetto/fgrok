package config

import "time"

const (
	// BufferSize is the default buffer size for network operations
	BufferSize = 4096

	// MaxConfigSize Define constant for max config file size (1MB)
	MaxConfigSize = 1 << 20 // 1MB

	// ShutdownTimeout is the timeout for graceful shutdown
	ShutdownTimeout = 10 * time.Second

	// MinHTTPParts is the minimum number of parts in HTTP request
	MinHTTPParts = 3

	// PingInterval is the interval to send ping messages
	PingInterval = 30 * time.Second

	// PongTimeout is the timeout waiting for pong response
	PongTimeout = 10 * time.Second

	// DefaultRequestTimeout is the timeout for HTTP requests
	DefaultRequestTimeout = 30 * time.Second

	// MaxBackoffAttempts is the maximum number of backoff attempts before using constant delay
	MaxBackoffAttempts = 5

	// MinTokenLength defines the minimum required length for authentication tokens
	MinTokenLength = 32

	// MetadataAuthorizationKey is the gRPC metadata key for authorization tokens
	MetadataAuthorizationKey = "authorization"

	// MetadataSubdomainKey is the gRPC metadata key for tunnel subdomains
	MetadataSubdomainKey = "tunnel-subdomain"

	// MetadataRemoteAddrKey is the gRPC metadata key for remote addresses
	MetadataRemoteAddrKey = "tunnel-remote-addr"
)
