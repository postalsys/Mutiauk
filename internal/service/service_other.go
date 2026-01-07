//go:build !linux

package service

import "fmt"

// isRootImpl returns false on non-Linux platforms.
func isRootImpl() bool {
	return false
}

// installImpl returns an error on non-Linux platforms.
func installImpl(cfg ServiceConfig, execPath string) error {
	return fmt.Errorf("service installation is only supported on Linux")
}

// uninstallImpl returns an error on non-Linux platforms.
func uninstallImpl(serviceName string) error {
	return fmt.Errorf("service management is only supported on Linux")
}

// statusImpl returns an error on non-Linux platforms.
func statusImpl(serviceName string) (string, error) {
	return "", fmt.Errorf("service management is only supported on Linux")
}

// isInstalledImpl returns false on non-Linux platforms.
func isInstalledImpl(serviceName string) bool {
	return false
}
