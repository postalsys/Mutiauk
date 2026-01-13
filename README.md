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
- Unix socket API for CLI-daemon communication
- Route persistence with `--persist` flag

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

# Check status (shows daemon info, config path, uptime)
mutiauk status

# Manage routes
mutiauk route list
mutiauk route add 10.0.0.0/8
mutiauk route add 10.0.0.0/8 --persist  # Save to config file
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

## Benchmarks

Performance benchmarks running through a Muti Metroo SOCKS5 tunnel:

| Protocol | Throughput | Duration | Notes |
|----------|------------|----------|-------|
| TCP | 1.42 Gbits/sec | 60s | 10 concurrent streams via iperf3 |
| UDP | 100 Mbits/sec | 60s | 0.09% packet loss, 0.2ms jitter |

**Test environment:** All components (TUN client, SOCKS5 ingress, exit node, echo
server) running on the same machine in Docker containers. Real-world performance
over network links will vary based on latency and bandwidth.

**Data path:**
```
Client -> TUN -> Mutiauk -> SOCKS5 -> Muti Metroo (QUIC) -> Exit -> Target
```

See [test/loadtest/](test/loadtest/) for the benchmark code and instructions.

## Building

```bash
# Build for Linux
make build-linux

# Build for ARM64
make build-linux-arm64

# Run tests
make test

# Run load tests
make loadtest-up      # Start test environment
make loadtest-shell   # Get shell in test container
make loadtest-run     # Run full test suite
make loadtest-down    # Stop test environment
```

## License

MIT License - see [LICENSE](LICENSE) for details.
