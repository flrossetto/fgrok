// Package server implements the main fgrok server with:
// - HTTP/HTTPS servers for redirection and reverse proxy
// - gRPC server for client communication
// - Automatic certificate management (Let's Encrypt)
// - Mediator integration for message routing
//
// Main components:
// - Server: Main structure orchestrating all services
// - startHTTPServer: HTTP server for HTTPS redirection
// - startHTTPSServer: Main HTTPS server with reverse proxy
// - startGRPCServer: gRPC server for tunneling
// - createTlsConfig: Automatic TLS configuration
package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"

	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"github.com/flrossetto/fgrok/internal/firsterr"
	"github.com/flrossetto/fgrok/internal/mediator"
	"github.com/flrossetto/fgrok/internal/proto"
	"github.com/flrossetto/fgrok/server/handler"
	"github.com/flrossetto/fgrok/server/interceptor"
	"github.com/flrossetto/fgrok/server/tunnel"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Server represents the main fgrok server instance.
// Manages all server subsystems and services.
//
// Fields:
// - logger: Logger for event recording
// - config: Server configuration
// - mediator: Mediator for internal message routing
type Server struct {
	logger   *logrus.Logger
	config   config.ServerConfig
	mediator mediator.Mediator
}

// NewServer creates a new server instance.
// Parameters:
// - logger: Configured logger
// - config: Server configuration
//
// Returns:
// - *Server: Ready-to-use instance
// Notes:
// - Initializes a new internal mediator
// - Logs initialization event
func NewServer(logger *logrus.Logger, config config.ServerConfig) *Server {
	logger.WithFields(logrus.Fields{
		"httpAddr":  config.HTTPAddr,
		"httpsAddr": config.HTTPSAddr,
		"grpcAddr":  config.GRPCAddr,
		"domain":    config.Domain,
	}).Info("Creating new server instance")

	return &Server{
		logger:   logger,
		config:   config,
		mediator: mediator.New(),
	}
}

// Start concurrently initializes all server components.
// Parameters:
// - ctx: Context for cancellation control
//
// Returns:
// - error: First error encountered or nil if terminated normally
//
// Behavior:
// - Starts HTTP, HTTPS and gRPC servers in separate goroutines
// - Uses firsterr for concurrency management
// - Returns on first error or when all complete
// - Performs orderly shutdown on context cancellation
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting server components")

	manager, tlsConfig := createTLSConfig(s.config)

	_, g := firsterr.WithContext(ctx)
	defer g.Cancel()

	g.Go(func(ctx context.Context) error {
		return s.startHTTPServer(ctx, manager)
	})
	g.Go(func(ctx context.Context) error {
		return s.startHTTPSServer(ctx, manager, tlsConfig)
	})
	g.Go(func(ctx context.Context) error {
		return s.startGRPCServer(ctx, tlsConfig)
	})

	if err := g.Wait(); err != nil {
		return fgrokerr.New(fgrokerr.CodeServerError, "server group failed", fgrokerr.WithError(err))
	}

	return nil
}

func (s *Server) startHTTPServer(ctx context.Context, manager *autocert.Manager) error {
	s.logger.WithFields(logrus.Fields{
		"addr": s.config.HTTPAddr,
	}).Info("Starting HTTP server (redirect to HTTPS)")

	server := &http.Server{
		Addr:              s.config.HTTPAddr,
		Handler:           manager.HTTPHandler(handler.NewHTTPHandler()),
		ReadHeaderTimeout: s.config.ReadHeaderTimeout,
	}

	errCh := make(chan error, 1)

	go (func() {
		if err := server.ListenAndServe(); err != nil && !fgrokerr.Is(err, http.ErrServerClosed) {
			s.logger.WithError(err).Error("HTTP server failed")
			errCh <- err
		} else {
			s.logger.Info("HTTP server stopped")
		}
		close(errCh)
	})()

	select {
	case err := <-errCh:
		return err

	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.ShutdownTimeout)
	defer cancel()

	err := server.Shutdown(shutdownCtx)
	if err != nil {
		return fgrokerr.New(fgrokerr.CodeShutdownError, "HTTP server shutdown failed", fgrokerr.WithError(err))
	}

	return nil
}

func (s *Server) startHTTPSServer(ctx context.Context, manager *autocert.Manager, tlsConfig *tls.Config) error {
	handler, err := handler.NewHTTPSHandler(s.logger, s.mediator)
	if err != nil {
		return err
	}

	defer handler.Shutdown()

	s.logger.WithFields(logrus.Fields{
		"addr": s.config.HTTPSAddr,
	}).Info("Starting HTTPS server")

	server := &http.Server{
		Addr:              s.config.HTTPSAddr,
		Handler:           manager.HTTPHandler(handler),
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: s.config.ReadHeaderTimeout,
	}

	errCh := make(chan error, 1)

	go (func() {
		if err := server.ListenAndServeTLS("", ""); err != nil && !fgrokerr.Is(err, http.ErrServerClosed) {
			s.logger.WithError(err).Error("HTTPS server failed")
			errCh <- err
		} else {
			s.logger.Info("HTTPS server stopped")
		}
		close(errCh)
	})()

	select {
	case err := <-errCh:
		return err

	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fgrokerr.New(fgrokerr.CodeShutdownError, "HTTPS server shutdown failed", fgrokerr.WithError(err))
	}

	return nil
}

func (s *Server) startGRPCServer(ctx context.Context, tlsConfig *tls.Config) error {
	s.logger.WithFields(logrus.Fields{
		"addr": s.config.GRPCAddr,
	}).Info("Starting gRPC server")

	authInterceptor := interceptor.NewAuthInterceptor(s.logger, s.config.Token)

	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsConfig)),
		grpc.ChainUnaryInterceptor(authInterceptor.Unary()),
		grpc.ChainStreamInterceptor(authInterceptor.Stream()), //nolint:contextcheck
	)

	proto.RegisterHttpTunnelServiceServer(
		server,
		tunnel.NewHTTPTunnel(s.logger, s.mediator),
	)

	proto.RegisterTcpTunnelServiceServer(
		server,
		tunnel.NewTCPTunnel(s.logger, s.mediator),
	)

	errCh := make(chan error, 1)

	go (func() {
		var lc net.ListenConfig

		lis, err := lc.Listen(ctx, "tcp", s.config.GRPCAddr)
		if err != nil {
			errCh <- err

			return
		}

		if err := server.Serve(lis); err != nil && !fgrokerr.Is(err, http.ErrServerClosed) {
			s.logger.WithError(err).Error("gRPC server failed")
			errCh <- err
		} else {
			s.logger.Info("gRPC server stopped")
		}
		close(errCh)
	})()

	select {
	case err := <-errCh:
		return err

	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.ShutdownTimeout)
	defer cancel()

	defer (func() {
		server.GracefulStop()
		cancel()
	})()

	<-shutdownCtx.Done()

	return nil
}

// createTlsConfig creates automatic TLS configuration for:
// - HTTPS server
// - gRPC server
//
// Parameters:
// - cfg: Server configuration
//
// Returns:
// - *autocert.Manager: Let's Encrypt certificate manager
// - *tls.Config: Ready-to-use TLS configuration
//
// Details:
// - Configures certificate cache in specified directory
// - Applies host policy based on configured domain
// - Supports HTTP/2 (h2) for gRPC
// - Requires TLS 1.2 as minimum version
func createTLSConfig(cfg config.ServerConfig) (*autocert.Manager, *tls.Config) {
	manager := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(cfg.CertDir),
		HostPolicy: func(_ context.Context, host string) error {
			h := strings.ToLower(strings.TrimSpace(host))
			if h == cfg.Domain || strings.HasSuffix(h, "."+cfg.Domain) {
				return nil
			}

			return fgrokerr.New(fgrokerr.CodeInvalidInput, "host not allowed by autocert")
		},
	}

	tlsConfig := &tls.Config{
		GetCertificate: manager.GetCertificate,
		NextProtos:     []string{"h2", acme.ALPNProto}, // h2 for gRPC, ACME ALPN for validation on 443 if needed
		MinVersion:     tls.VersionTLS12,
	}

	return manager, tlsConfig
}
