package cli

import (
	"fmt"
	"runtime"

	"github.com/coinstash/mutiauk/internal/service"
	"github.com/spf13/cobra"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "System service management (Linux only)",
		Long: `Install, uninstall, and manage Mutiauk as a systemd service.

Mutiauk requires root privileges for TUN interface creation, so it must
be installed as a system service (not a user service).`,
	}

	cmd.AddCommand(newServiceInstallCmd())
	cmd.AddCommand(newServiceUninstallCmd())
	cmd.AddCommand(newServiceStatusCmd())

	return cmd
}

func newServiceInstallCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install as systemd service",
		Long: `Install Mutiauk as a systemd service.

The service will be enabled and started automatically. It will also
start on system boot.

Example:
  sudo mutiauk service install -c /etc/mutiauk/config.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "linux" {
				return fmt.Errorf("service installation is only supported on Linux")
			}

			if !service.IsRoot() {
				return fmt.Errorf("must run as root to install service\n\nTry: sudo mutiauk service install -c %s", configPath)
			}

			cfg := service.DefaultConfig(configPath)
			return service.Install(cfg)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "/etc/mutiauk/config.yaml", "Path to config file")

	return cmd
}

func newServiceUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall systemd service",
		Long: `Remove Mutiauk systemd service.

This will stop and disable the service, then remove the systemd unit file.

Example:
  sudo mutiauk service uninstall`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "linux" {
				return fmt.Errorf("service management is only supported on Linux")
			}

			if !service.IsRoot() {
				return fmt.Errorf("must run as root to uninstall service\n\nTry: sudo mutiauk service uninstall")
			}

			return service.Uninstall("mutiauk")
		},
	}

	return cmd
}

func newServiceStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show systemd service status",
		Long: `Show the current status of the Mutiauk systemd service.

Example:
  mutiauk service status`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "linux" {
				return fmt.Errorf("service management is only supported on Linux")
			}

			if !service.IsInstalled("mutiauk") {
				fmt.Println("Service is not installed")
				fmt.Println("\nTo install: sudo mutiauk service install -c /etc/mutiauk/config.yaml")
				return nil
			}

			status, err := service.Status("mutiauk")
			if err != nil {
				return err
			}

			fmt.Printf("Service status: %s\n", status)
			return nil
		},
	}

	return cmd
}
