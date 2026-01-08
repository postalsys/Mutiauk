# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**Note:** The current year is 2026. Keep documentation and references up to date accordingly.

## Project Overview

Mutiauk is a TUN-based SOCKS5 proxy agent for Kali Linux. It creates a virtual network interface (TUN) and routes selected TCP/UDP traffic through a SOCKS5 proxy using Google's gVisor userspace TCP/IP stack. The project is a single Go binary with two modes: daemon (traffic forwarding) and CLI (route management).

## Build Commands

```bash
# Build for Linux (required - TUN only works on Linux)
make build-linux

# Build for current platform (limited functionality on non-Linux)
make build

# Run all tests
make test

# Run specific package tests
go test -v ./internal/socks5/...

# Run single test
go test -v -run TestName ./internal/socks5/

# Lint (requires golangci-lint)
make lint

# Format code
make fmt

# Tidy dependencies
make tidy
```

## Development Environment

**Always use Docker for building and testing** unless explicitly requested otherwise. TUN requires Linux with CAP_NET_ADMIN, so Docker is essential for development on macOS.

```bash
# Start full test environment (Kali + SOCKS5 proxy + nginx target)
make docker-test

# Get shell in Kali container for manual testing
make docker-shell

# View logs
make logs
```

## Code Style

- **ASCII only**: Do not use emojis or non-ASCII characters in code, comments, documentation, commit messages, or any other text files.

## Architecture

### Data Flow

```
TCP: Application → TUN → gVisor Stack → TCP Forwarder → SOCKS5 CONNECT → Target
UDP: Application → TUN → Intercept Endpoint → Raw UDP Forwarder → SOCKS5 UDP ASSOCIATE → Target
```

### Core Components

**daemon/server.go** - Orchestrator that wires all components together. Entry point is `Server.Run()` which:
1. Creates TUN device
2. Initializes gVisor stack with TCP/UDP handlers
3. Sets up NAT table for connection tracking
4. Applies routes via netlink
5. Handles SIGHUP for hot reload

**stack/stack.go** - Wraps gVisor's `tcpip/stack` package. Uses `tcp.NewForwarder` to intercept TCP connections, converts them to `net.Conn` via `gonet`, then passes to handlers. UDP is handled separately via link-layer interception.

**stack/intercept_endpoint.go** - Wraps gVisor's link endpoint to intercept UDP packets at the link layer. This is necessary because gVisor's `udp.NewForwarder` only handles packets destined for local addresses, not outbound traffic. The intercept endpoint parses raw IP/UDP headers and passes payloads to the `RawUDPHandler`.

**proxy/tcp_forwarder.go** - Implements `TCPHandler` interface. Establishes SOCKS5 CONNECT and performs bidirectional data copy. Supports `PreConnect` for accurate port scanning.

**proxy/udp_forwarder.go** - Legacy UDP handler using gVisor's UDP forwarder (for local-destined packets).

**proxy/raw_udp_forwarder.go** - Implements `RawUDPHandler` interface for intercepted UDP. Uses SOCKS5 UDP ASSOCIATE to relay datagrams. Maintains a pool of relay sessions with TTL-based cleanup.

**socks5/client.go** - SOCKS5 client supporting CONNECT (TCP) and UDP ASSOCIATE. Handles authentication (none or username/password).

**nat/table.go** - Connection tracking with TTL-based expiration. Maps (protocol, src, dst) → proxy connection. Background GC goroutine cleans stale entries.

**route/manager.go** - Linux route management via netlink. Supports idempotent add/remove, plan/diff for declarative route management, conflict detection for overlapping CIDRs.

**tun/device_linux.go** - TUN device creation using `/dev/net/tun` with `TUNSETIFF` ioctl. Non-Linux platforms get a stub that returns an error.

**config/watcher.go** - Hot reload via fsnotify with debouncing.

### Key Interfaces

```go
// stack/stack.go - Implemented by proxy forwarders
type TCPHandler interface {
    HandleTCP(ctx context.Context, conn net.Conn, srcAddr, dstAddr net.Addr) error
}

// TCPPreConnector - Optional interface for accurate port scanning
type TCPPreConnector interface {
    PreConnect(ctx context.Context, srcAddr, dstAddr net.Addr) (net.Conn, error)
}

// RawUDPHandler - For link-layer intercepted UDP packets
type RawUDPHandler interface {
    HandleRawUDP(ctx context.Context, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) ([]byte, error)
}
```

## Platform Constraints

- **Linux only**: TUN device and netlink route management require Linux
- **Root required**: TUN creation and route manipulation need CAP_NET_ADMIN
- **gVisor dependency**: Uses `gvisor.dev/gvisor/pkg/tcpip` for userspace networking

## Configuration

Config file: YAML format, see `configs/mutiauk.example.yaml`

Key sections:
- `tun`: Interface name, MTU, IPv4/IPv6 addresses
- `socks5`: Proxy server, auth credentials, timeouts
- `routes`: CIDR destinations to forward through proxy
- `nat`: Connection table size and timeouts

## Documentation

Mutiauk documentation is part of the Muti Metroo documentation site:
- **Source:** `../Muti-Metroo-v4/docs/docs/mutiauk.mdx`
- **URL:** https://mutimetroo.com/mutiauk/

Edit documentation in the Muti Metroo docs directory.

## Releasing

Releases are automated via GitHub Actions using [release-please](https://github.com/googleapis/release-please).

### Commit Message Format

Use [Conventional Commits](https://www.conventionalcommits.org/) format:

- `feat: add new feature` - Minor version bump
- `fix: fix a bug` - Patch version bump
- `feat!: breaking change` or `BREAKING CHANGE:` in body - Major version bump
- `chore:`, `docs:`, `refactor:`, `test:` - No version bump

### Release Process

1. Merge PRs with conventional commit messages to `master`
2. release-please automatically creates a Release PR with:
   - Version bump based on commit types
   - Updated CHANGELOG.md
3. Merge the Release PR to trigger:
   - Git tag creation
   - Binary builds (Linux amd64/arm64)
   - UPX compression
   - GitHub Release with assets and checksums

### Manual Testing

Test infrastructure is in the `test/` directory:
- Start environment: `make docker-test`
- Get shell: `make docker-shell`
