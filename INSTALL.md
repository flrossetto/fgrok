# fgrok Installation and Configuration

## Prerequisites

- Go 1.20+ (for compilation only)
- Git (for compilation only)
- Systemd (for running as a service)

## Installation

### Method 1: Via Go install (recommended)

```bash
go install github.com/flrossetto/fgrok/cmd/fgrok@latest
```

The binary will be installed at `$GOPATH/bin/fgrok`

### Method 2: Manual build

```bash
git clone https://github.com/flrossetto/fgrok.git
cd fgrok
make build
```

The binary will be generated at `bin/fgrok`

## Configuration

1. (Optional) Create a configuration file from the example:

```bash
cp example.fgrok.yaml .fgrok.yaml
```

Note: The configuration file is optional. If not provided, fgrok will automatically look for:
- `.fgrok.yaml` in the current directory
- `~/.fgrok.yaml` in the user's home directory

2. Edit the configuration file with your settings (if created):

```yaml
server:
  token: "your_32_character_secret_token"
  httpAddr: ":80"
  httpsAddr: ":443"
  grpcAddr: ":50051"
  domain: "yourdomain.com"
  certDir: "./certs"

client:
  token: "your_32_character_secret_token"
  serverAddr: "server.yourdomain.com:50051"
  serverName: "server.yourdomain.com"
  tunnels:
    - type: "http"
      subdomain: "myservice"
      localAddr: "localhost:8080"
```

## Systemd Service Configuration

### For the Server

1. Create a service file at `/etc/systemd/system/fgrok-server.service`:

```ini
[Unit]
Description=fgrok Reverse Tunnel Server
After=network.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=/opt/fgrok
ExecStart=/opt/fgrok/bin server --config /opt/fgrok/config.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

2. System configuration:

```bash
sudo mkdir -p /etc/fgrok
sudo cp .fgrok.yaml /etc/fgrok/config.yaml
sudo cp bin/fgrok /usr/local/bin/
sudo useradd -r -s /bin/false fgrok
sudo chown -R fgrok:fgrok /etc/fgrok
```

3. Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable fgrok-server
sudo systemctl start fgrok-server
```

### Running the Client

You can run the client in two ways:

1. With custom config file:
```bash
fgrok client --config /path/to/custom-config.yaml
```

2. Without config file (uses auto-detection):
```bash
fgrok client
```

For persistent connections, consider using terminal multiplexers:

```bash
# With custom config:
screen -S fgrok-client -d -m fgrok client --config /path/to/config.yaml
# Or
tmux new -d -s fgrok-client "fgrok client --config /path/to/config.yaml"

# Without config (auto-detection):
screen -S fgrok-client -d -m fgrok client
# Or
tmux new -d -s fgrok-client "fgrok client"
```

## Service Verification

Check server status:

```bash
sudo systemctl status fgrok-server
```

View server logs:

```bash
sudo journalctl -u fgrok-server -f
```

## Updating

1. Stop the server service:

```bash
sudo systemctl stop fgrok-server
```

2. Update the binary:

```bash
go install github.com/flrossetto/fgrok/cmd/fgrok@latest
# Or
make build && sudo cp bin/fgrok /usr/local/bin/
```

3. Restart the server service:

```bash
sudo systemctl start fgrok-server
```

For client updates, simply restart any running client processes with the new binary.
