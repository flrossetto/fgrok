// Package interceptor implements gRPC interceptors for authentication and request processing
package interceptor

import (
	"context"

	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// AuthInterceptor implements gRPC server interceptor for token authentication
type AuthInterceptor struct {
	logger *logrus.Logger
	token  string
}

// NewAuthInterceptor creates a new authentication interceptor
func NewAuthInterceptor(logger *logrus.Logger, token string) *AuthInterceptor {
	return &AuthInterceptor{
		logger: logger,
		token:  token,
	}
}

// Unary returns a unary server interceptor for authentication
func (i *AuthInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := i.authenticate(ctx); err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

// Stream returns a stream server interceptor for authentication
func (i *AuthInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := i.authenticate(stream.Context()); err != nil {
			return err
		}

		return handler(srv, stream)
	}
}

// authenticate verifies the token from the request metadata
func (i *AuthInterceptor) authenticate(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		i.logger.Warn("Missing metadata in request")

		return fgrokerr.ToGRPC(fgrokerr.New(fgrokerr.CodeUnauthorized, "missing credentials"))
	}

	tokens := md.Get(config.MetadataAuthorizationKey)
	if len(tokens) == 0 {
		i.logger.Warn("Missing authorization token")

		return fgrokerr.ToGRPC(fgrokerr.New(fgrokerr.CodeUnauthorized, "missing authorization token"))
	}

	token := tokens[0]
	if token != i.token {
		i.logger.Warn("Invalid authorization token")

		return fgrokerr.ToGRPC(fgrokerr.New(fgrokerr.CodeUnauthorized, "invalid authorization token"))
	}

	return nil
}
