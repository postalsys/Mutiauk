# Mutiauk

TUN-based SOCKS5 proxy agent for Linux.

Mutiauk creates a virtual network interface (TUN) and routes selected TCP/UDP
traffic through a SOCKS5 proxy using Google's gVisor userspace TCP/IP stack.

## Features

- Route specific CIDRs through SOCKS5 proxy
- TCP and UDP forwarding support
- Hot reload configuration via SIGHUP
- Userspace networking (no kernel modifications)
- Automatic route fetching from Muti Metroo API

## Requirements

- Linux with TUN support
- Root privileges (CAP_NET_ADMIN)
- SOCKS5 proxy server

## Installation

Download the latest release from the
[Releases](https://github.com/postalsys/Mutiauk/releases) page.

```bash
# Download
curl -L -o mutiauk https://github.com/postalsys/Mutiauk/releases/latest/download/mutiauk-linux-amd64
chmod +x mutiauk
sudo mv mutiauk /usr/local/bin/
```

## Usage

```bash
# Start daemon with config file
sudo mutiauk daemon -c /etc/mutiauk/config.yaml

# Manage routes (CLI mode)
mutiauk route list
mutiauk route add 10.0.0.0/8
mutiauk route remove 10.0.0.0/8

# Analyze routing for a destination
mutiauk route trace 10.10.5.100
mutiauk route trace internal.corp.local --json
```

## Configuration

See [configs/mutiauk.example.yaml](configs/mutiauk.example.yaml) for a complete
example configuration.

```yaml
tun:
  name: tun0
  mtu: 1500
  address: 10.200.200.1/24

socks5:
  server: proxy.example.com:1080

routes:
  - destination: 192.168.0.0/16
    enabled: true
  - destination: 10.0.0.0/8
    enabled: true

# Optional: automatic route fetching from Muti Metroo
autoroutes:
  enabled: true
  url: "http://localhost:8080"
  poll_interval: 30s
```

## Documentation

Full documentation: https://mutimetroo.com/mutiauk/

## Building

```bash
# Build for Linux
make build-linux

# Build for ARM64
make build-linux-arm64

# Run tests
make test
```

## License

MIT License - see [LICENSE](LICENSE) for details.
