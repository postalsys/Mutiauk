//go:build !linux

package tun

import (
	"fmt"
	"runtime"
)

// New returns an error on non-Linux platforms
func New(cfg Config) (Device, error) {
	return nil, fmt.Errorf("TUN device is only supported on Linux, current platform: %s", runtime.GOOS)
}
