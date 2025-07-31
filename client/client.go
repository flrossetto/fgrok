// Package client implements the fgrok tunneling client.
//
// Provides functionality for:
// - Establishing secure connection with fgrok server via gRPC
// - Managing multiple tunnels (HTTP and TCP)
// - Forwarding traffic between local and remote servers
package client

import (
	"context"
	"crypto/tls"

	"github.com/flrossetto/fgrok/client/interceptor"
	"github.com/flrossetto/fgrok/client/tunnel"
	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"github.com/flrossetto/fgrok/internal/firsterr"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

// Client represents the main tunneling client.
type Client struct {
	logger *logrus.Logger
	config config.ClientConfig
	conn   *grpc.ClientConn
}

// New creates a new Client instance.
//
// Parameters:
//   - logger: Logger for event recording
//   - config: Client configuration
//
// Returns:
//   - *Client: Client instance
//   - error: Error if connection fails
func New(logger *logrus.Logger, cfg config.ClientConfig) (*Client, error) {
	logger.WithFields(logrus.Fields{
		"serverAddr": cfg.ServerAddr,
		"serverName": cfg.ServerName,
		"tunnels":    len(cfg.Tunnels),
	}).Info("Creating new client instance")

	tlsCfg := &tls.Config{
		ServerName: cfg.ServerName,
		MinVersion: tls.VersionTLS12,
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                config.PingInterval, // sends ping if inactive
			Timeout:             config.PongTimeout,  // waits for pong
			PermitWithoutStream: true,
		}),
		grpc.WithStreamInterceptor(interceptor.StreamInterceptor(logger)),
	}

	conn, err := grpc.NewClient(cfg.ServerAddr, dialOpts...)
	if err != nil {
		logger.WithError(err).WithFields(logrus.Fields{
			"serverAddr": cfg.ServerAddr,
			"serverName": cfg.ServerName,
		}).Error("Failed to establish gRPC connection")

		return nil, fgrokerr.New(fgrokerr.CodeConnectionError, "failed to connect", fgrokerr.WithError(err))
	}

	logger.WithFields(logrus.Fields{
		"serverAddr": cfg.ServerAddr,
		"serverName": cfg.ServerName,
	}).Info("gRPC connection established successfully")

	return &Client{
		logger: logger,
		config: cfg,
		conn:   conn,
	}, nil
}

// Start initiates all configured tunnels.
//
// Parameters:
//   - ctx: Context for cancellation control
//
// Returns:
//   - error: Error if any tunnel fails
//
// The method:
// - Creates a goroutine group to manage tunnels
// - Supports HTTP and TCP tunnels
// - Waits for all tunnels to complete or first error
func (c *Client) Start(ctx context.Context) error {
	c.logger.Info("Starting client tunnels")

	ctx = metadata.AppendToOutgoingContext(ctx,
		config.MetadataAuthorizationKey, c.config.Token,
	)

	_, g := firsterr.WithContext(ctx)
	defer g.Cancel()

	for i, tn := range c.config.Tunnels {
		switch tn.Type {
		case "http":
			log := c.logger.WithFields(logrus.Fields{
				"tunnelNum": i + 1,
				"type":      tn.Type,
				"subdomain": tn.Subdomain,
				"localAddr": tn.LocalAddr,
			})
			log.Info("Starting tunnel")

			g.Go(func(ctx context.Context) error {
				return tunnel.NewHTTPTunnel(log, tn, c.conn).Start(ctx)
			})

		case "tcp":
			log := c.logger.WithFields(logrus.Fields{
				"tunnelNum":  i + 1,
				"type":       tn.Type,
				"remoteAddr": tn.RemoteAddr,
				"localAddr":  tn.LocalAddr,
			})
			log.Info("Starting tunnel")

			g.Go(func(ctx context.Context) error {
				return tunnel.NewTCPTunnel(log, tn, c.conn).Start(ctx)
			})

		default:
			c.logger.WithField("type", tn.Type).Error("Invalid tunnel type")

			return fgrokerr.New(fgrokerr.CodeTunnelError, "invalid tunnel type: "+tn.Type)
		}
	}

	err := g.Wait()
	if err != nil {
		return fgrokerr.New(fgrokerr.CodeTunnelError, "tunnel group failed", fgrokerr.WithError(err))
	}

	return nil
}
