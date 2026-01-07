// Package service provides systemd service management for Mutiauk.
// Mutiauk requires root privileges for TUN interface creation, so only
// system-level service installation is supported (no user-level services).
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ServiceConfig holds configuration for installing the service.
type ServiceConfig struct {
	// Name is the systemd service name
	Name string

	// Description is the service description
	Description string

	// ConfigPath is the absolute path to the config file
	ConfigPath string

	// WorkingDir is the working directory for the service
	WorkingDir string
}

// DefaultConfig returns a default service configuration.
func DefaultConfig(configPath string) ServiceConfig {
	absPath, _ := filepath.Abs(configPath)
	workDir := filepath.Dir(absPath)

	return ServiceConfig{
		Name:        "mutiauk",
		Description: "Mutiauk TUN-based SOCKS5 proxy agent",
		ConfigPath:  absPath,
		WorkingDir:  workDir,
	}
}

// IsRoot returns true if the current process is running as root.
func IsRoot() bool {
	return isRootImpl()
}

// Install installs Mutiauk as a systemd service.
// Requires root privileges.
func Install(cfg ServiceConfig) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("service installation is only supported on Linux")
	}

	if !IsRoot() {
		return fmt.Errorf("must run as root to install service")
	}

	// Get the executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks to get the real path
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	return installImpl(cfg, execPath)
}

// Uninstall removes the systemd service.
// Requires root privileges.
func Uninstall(serviceName string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("service management is only supported on Linux")
	}

	if !IsRoot() {
		return fmt.Errorf("must run as root to uninstall service")
	}

	return uninstallImpl(serviceName)
}

// Status returns the current status of the service.
func Status(serviceName string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("service management is only supported on Linux")
	}

	return statusImpl(serviceName)
}

// IsInstalled checks if the service is already installed.
func IsInstalled(serviceName string) bool {
	if runtime.GOOS != "linux" {
		return false
	}

	return isInstalledImpl(serviceName)
}

// runCommand executes a command and returns combined output.
func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
