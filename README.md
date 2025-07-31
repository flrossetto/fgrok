# fgrok - Reverse Tunnel in Go

[![Go Report Card](https://goreportcard.com/badge/github.com/flrossetto/fgrok)](https://goreportcard.com/report/github.com/flrossetto/fgrok)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

fgrok is a reverse tunnel service that allows exposing local services to the internet through an intermediary server.

## Features

- HTTP/HTTPS tunnels with custom subdomains support
- Generic TCP tunnels
- Token-based authentication
- Automatic reconnection
- Detailed logging
- YAML configuration

For detailed installation and systemd service configuration, see [INSTALL.md](INSTALL.md).

## Usage

### Server
```bash
fgrok server --config .fgrok.yaml
```

### Client
```bash
fgrok client --config .fgrok.yaml
```

## Architecture

```
Client <-> [fgrok Client] <-> [fgrok Server] <-> Internet
```

### Main Components

- **Client**: Establishes connection with server and manages tunnels
- **Server**: Receives connections and routes traffic
- **Tunnels**: HTTP and TCP
- **Interceptor**: Authentication middleware
- **Mediator**: Mediator pattern for internal communication

## Requirements

- Go 1.20+
- TLS certificates (for HTTPS)

## License

MIT - See [LICENSE](LICENSE) for details.
