// Package config handles configuration management for fgrok.
//
// Provides functionality for:
// - Loading configuration from YAML files
// - Validating configuration structures
// - Managing both client and server configurations
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/flrossetto/fgrok/internal/fgrokerr"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// AppConfig represents the complete fgrok configuration structure.
type AppConfig struct {
	// Client Client-specific configuration
	Client ClientConfig `yaml:"client"`
	// Server Server-specific configuration
	Server ServerConfig `yaml:"server"`
}

// ServerConfig holds server configuration parameters.
type ServerConfig struct {
	// Authentication token (min 32 chars)
	Token string `yaml:"token"`
	// Address to listen for HTTP traffic (e.g. ":80")
	HTTPAddr string `yaml:"httpAddr"`
	// Address to listen for HTTPS traffic (e.g. ":443")
	HTTPSAddr string `yaml:"httpsAddr"`
	// Address for gRPC server communication
	GRPCAddr string `yaml:"grpcAddr"`
	// Base domain name for all tunnels
	Domain string `yaml:"domain"`
	// Path to directory containing TLS certificates
	CertDir string `yaml:"certDir"`
	// Controls logging verbosity
	LogLevel logrus.Level `yaml:"logLevel"`

	// Maximum duration to wait for reading HTTP headers (e.g. "30s", "1m")
	// If zero, uses the default HTTP server timeout
	ReadHeaderTimeout time.Duration `yaml:"readHeaderTimeout"`
}

// ClientConfig holds client configuration parameters.
type ClientConfig struct {
	// Authentication token (min 32 chars)
	Token string `yaml:"token"`
	// Server address to connect to (host:port)
	ServerAddr string `yaml:"serverAddr"`
	// Expected TLS server name for verification
	ServerName string `yaml:"serverName"`
	// Controls logging verbosity
	LogLevel logrus.Level `yaml:"logLevel"`
	// List of configured tunnels
	Tunnels []TunnelConfig `yaml:"tunnels"`
}

// TunnelConfig defines configuration for a single tunnel.
type TunnelConfig struct {
	// Type of tunnel ("http" or "tcp")
	Type string `yaml:"type"`
	// Subdomain prefix for HTTP tunnels
	Subdomain string `yaml:"subdomain"`
	// Local service address to forward to
	LocalAddr string `yaml:"localAddr"`
	// Remote port for TCP tunnels
	RemoteAddr string `yaml:"remoteAddr,omitempty"`
}

// LoadConfig loads configuration from YAML file.
//
// Parameters:
//   - path: Path to configuration file (if empty, searches for .fgrok.yaml in current dir then user home)
//   - cfg: Pointer to Config struct to populate
//
// Returns:
//   - error: Any error encountered during loading
//
// The function:
// - Uses findConfigFile to locate the config file
// - Resolves absolute path
// - Validates path is within allowed directories
// - Reads file contents safely
// - Unmarshals YAML into config struct
func LoadConfig(path string, cfg *AppConfig) error {
	foundPath, err := findConfigFile(path)
	if err != nil {
		return err
	}

	absPath, err := filepath.Abs(foundPath)
	if err != nil {
		return fgrokerr.New(fgrokerr.CodeConfigError, "invalid config path", fgrokerr.WithError(err))
	}

	// Open file safely
	file, err := os.Open(filepath.Clean(absPath))
	if err != nil {
		return fgrokerr.New(fgrokerr.CodeConfigError, "failed to open config", fgrokerr.WithError(err))
	}

	defer func() {
		if err := file.Close(); err != nil {
			logrus.WithError(err).Warn("Failed to close config file")
		}
	}()

	// Read file with size limit
	data, err := io.ReadAll(io.LimitReader(file, MaxConfigSize))
	if err != nil {
		return fgrokerr.New(fgrokerr.CodeConfigError, "failed to read config", fgrokerr.WithError(err))
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fgrokerr.New(fgrokerr.CodeConfigError, "invalid config format", fgrokerr.WithError(err))
	}

	return nil
}

// findConfigFile locates the configuration file using the following precedence:
// 1. If path is provided, uses that
// 2. Looks for .fgrok.yaml in current directory
// 3. Looks for ~/.fgrok.yaml
// Returns the found path or error if no config file found
func findConfigFile(path string) (string, error) {
	if path != "" {
		return path, nil
	}

	// Try local .fgrok.yaml first
	localPath := ".fgrok.yaml"
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	// Try global config in home dir
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fgrokerr.New(fgrokerr.CodeConfigError, "failed to get user home directory", fgrokerr.WithError(err))
	}

	globalPath := filepath.Join(home, ".fgrok.yaml")
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath, nil
	}

	return "", fgrokerr.New(fgrokerr.CodeConfigError, "no config file found (tried .fgrok.yaml in current dir and ~/.fgrok.yaml)")
}

// ValidateServerConfig validates server configuration.
//
// Parameters:
//   - cfg: ServerConfig to validate
//
// Returns:
//   - error: First validation error found
//
// Checks required fields:
// - HTTPAddr
// - HTTPSAddr
// - GRPCAddr
// - Domain
// - CertDir
func ValidateServerConfig(cfg ServerConfig) error {
	if cfg.HTTPAddr == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "httpAddr is required")
	}

	if cfg.HTTPSAddr == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "httpsAddr is required")
	}

	if cfg.GRPCAddr == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "grpcAddr is required")
	}

	if cfg.Domain == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "domain is required")
	}

	if cfg.Token == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "token is required")
	}

	if len(cfg.Token) < MinTokenLength {
		return fgrokerr.New(fgrokerr.CodeConfigError, "token must be at least 32 characters")
	}

	if cfg.CertDir == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "certDir is required")
	}

	if cfg.ReadHeaderTimeout < 0 {
		return fgrokerr.New(fgrokerr.CodeConfigError, "readHeaderTimeout cannot be negative")
	}

	if cfg.ReadHeaderTimeout > 5*time.Minute {
		return fgrokerr.New(fgrokerr.CodeConfigError, "readHeaderTimeout cannot exceed 5 minutes")
	}

	return nil
}

// validateHTTPTunnel checks requirements for HTTP tunnels
func validateHTTPTunnel(t TunnelConfig) error {
	if t.Subdomain == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "subdomain is required for http tunnels")
	}

	if t.LocalAddr == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "localAddr is required for http tunnels")
	}

	return nil
}

// validateTCPTunnel checks requirements for TCP tunnels
func validateTCPTunnel(t TunnelConfig) error {
	if t.RemoteAddr == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "remoteAddr is required for tcp tunnels")
	}

	if t.LocalAddr == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "localAddr is required for tcp tunnels")
	}

	return nil
}

// validateTunnel checks tunnel configuration based on type
func validateTunnel(t TunnelConfig) error {
	switch t.Type {
	case "http":
		return validateHTTPTunnel(t)
	case "tcp":
		return validateTCPTunnel(t)
	default:
		return fgrokerr.New(fgrokerr.CodeConfigError,
			fmt.Sprintf("invalid tunnel type: %s (must be 'http' or 'tcp')", t.Type))
	}
}

// ValidateClientConfig validates client configuration.
//
// Parameters:
//   - cfg: ClientConfig to validate
//
// Returns:
//   - error: First validation error found
//
// Checks:
// - Required fields (ServerAddr, ServerName)
// - At least one tunnel configured
// - Tunnel-specific requirements
func ValidateClientConfig(cfg ClientConfig) error {
	if cfg.ServerAddr == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "serverAddr is required")
	}

	if cfg.ServerName == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "serverName is required")
	}

	if cfg.Token == "" {
		return fgrokerr.New(fgrokerr.CodeConfigError, "token is required")
	}

	if len(cfg.Token) < MinTokenLength {
		return fgrokerr.New(fgrokerr.CodeConfigError, "token must be at least 32 characters")
	}

	if len(cfg.Tunnels) == 0 {
		return fgrokerr.New(fgrokerr.CodeConfigError, "at least one tunnel must be configured")
	}

	for _, t := range cfg.Tunnels {
		if err := validateTunnel(t); err != nil {
			return err
		}
	}

	return nil
}
