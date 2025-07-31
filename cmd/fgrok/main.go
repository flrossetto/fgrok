// Package main provides the entry point for the fgrok tunneling application.
//
// The main package:
// - Parses configuration
// - Initializes logging
// - Starts client or server based on flags
package main

import (
	"os"

	"github.com/flrossetto/fgrok/client"
	"github.com/flrossetto/fgrok/config"
	"github.com/flrossetto/fgrok/server"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

//nolint:gochecknoglobals
var rootCmd = &cobra.Command{
	Use:   "fgrok",
	Short: "CLI for fgrok management",
	Long:  `Unified client/server application for simple self-hosted reverse tunnels`,
}

//nolint:gochecknoglobals
var log = logrus.New()

//nolint:gochecknoglobals
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the fgrok server",
	Run: func(cmd *cobra.Command, _ []string) {
		configPath, _ := cmd.Flags().GetString("config")

		var cfg config.AppConfig
		if err := config.LoadConfig(configPath, &cfg); err != nil {
			log.WithError(err).Fatal("Failed to load config")
		}

		log.SetLevel(cfg.Server.LogLevel)

		if err := config.ValidateServerConfig(cfg.Server); err != nil {
			log.WithError(err).Fatal("Invalid server configuration")
		}

		log.Info("Starting fgrok server")
		if err := server.NewServer(log, cfg.Server).Start(cmd.Context()); err != nil {
			log.WithError(err).Fatal("Failed to start server")
		}
	},
}

//nolint:gochecknoglobals
var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Run the fgrok client",
	Run: func(cmd *cobra.Command, _ []string) {
		configPath, _ := cmd.Flags().GetString("config")

		var cfg config.AppConfig
		if err := config.LoadConfig(configPath, &cfg); err != nil {
			log.WithError(err).Fatal("Failed to load config")
		}

		log.SetLevel(cfg.Client.LogLevel)

		if err := config.ValidateClientConfig(cfg.Client); err != nil {
			log.WithError(err).Fatal("Invalid client configuration")
		}

		cli, err := client.New(log, cfg.Client)
		if err != nil {
			log.WithError(err).Fatal("Failed to connect")
		}

		log.Info("Connected to server, starting tunnel")
		if err := cli.Start(cmd.Context()); err != nil {
			log.WithError(err).Fatal("Tunnel failed")
		}
	},
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)

	serverCmd.Flags().String("config", "", "Path to server config file")
	clientCmd.Flags().String("config", "", "Path to client config file")

	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(clientCmd)

	if err := rootCmd.Execute(); err != nil {
		panic(err)
		// os.Exit(1)
	}
}
