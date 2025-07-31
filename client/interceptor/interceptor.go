// Package interceptor implements stream interceptors for fgrok client.
package interceptor

import (
	"context"
	"time"

	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// streamInterceptor implements client stream interceptor with reconnection logic
type streamInterceptor struct {
	logger *logrus.Logger
}

// StreamInterceptor creates a new stream interceptor instance
func StreamInterceptor(logger *logrus.Logger) grpc.StreamClientInterceptor {
	return (&streamInterceptor{logger: logger}).intercept
}

// intercept handles the stream interception logic with retry
//
//nolint:ireturn
func (si *streamInterceptor) intercept(
	ctx context.Context,
	desc *grpc.StreamDesc,
	cc *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	var attempt int

	for {
		attempt++

		// Check if context is cancelled
		if ctx.Err() != nil {
			return nil, fgrokerr.New(
				fgrokerr.CodeConnectionError,
				"context cancelled",
				fgrokerr.WithError(ctx.Err()),
			)
		}

		// Get the original stream
		stream, err := streamer(ctx, desc, cc, method, opts...)
		if err == nil {
			return &retryStream{
				ctx:          ctx,
				ClientStream: stream,
				desc:         desc,
				cc:           cc,
				method:       method,
				streamer:     streamer,
				opts:         opts,
				logger:       si.logger,
			}, nil
		}

		st, ok := status.FromError(err)
		if !ok {
			return nil, err
		}

		if !isRecoverable(st.Code()) {
			return nil, err
		}

		si.logger.WithError(err).Warnf("Stream creation failed (attempt %d), retrying...", attempt)

		attemptSleep(attempt)
	}
}

// retryStream wraps the gRPC client stream with reconnection logic
type retryStream struct {
	grpc.ClientStream

	ctx      context.Context //nolint:containedctx
	desc     *grpc.StreamDesc
	cc       *grpc.ClientConn
	method   string
	streamer grpc.Streamer
	opts     []grpc.CallOption
	logger   *logrus.Logger
}

// RecvMsg implements reconnection logic on stream receive
func (rs *retryStream) RecvMsg(m any) error {
	var attempt int

	for {
		attempt++
		// Check if context is cancelled
		if rs.ctx.Err() != nil {
			return fgrokerr.New(
				fgrokerr.CodeConnectionError,
				"context cancelled",
				fgrokerr.WithError(rs.ctx.Err()),
			)
		}

		err := rs.ClientStream.RecvMsg(m)
		if err == nil {
			return nil
		}

		st, ok := status.FromError(err)
		if !ok {
			return err
		}

		if !isRecoverable(st.Code()) {
			return err
		}

		attemptSleep(attempt)
		rs.logger.WithError(err).Warnf("Stream recv error (attempt %d), retrying...", attempt)

		newStream, serr := rs.streamer(rs.ctx, rs.desc, rs.cc, rs.method, rs.opts...)
		if serr != nil {
			rs.logger.WithError(serr).Error("Failed to reconnect stream")

			continue
		}

		rs.ClientStream = newStream
		rs.logger.Info("Stream reconnected successfully")
	}
}

func attemptSleep(attempt int) {
	d := time.Second

	if attempt < config.MaxBackoffAttempts {
		d = time.Duration(attempt) * time.Second
	}

	time.Sleep(d)
}

// isRecoverable checks if the error code is recoverable
func isRecoverable(code codes.Code) bool {
	switch code {
	case codes.Unauthenticated, codes.AlreadyExists:
		return false

	default:
		return true
	}
}
