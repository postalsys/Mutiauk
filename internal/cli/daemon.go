package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/postalsys/mutiauk/internal/config"
	"github.com/postalsys/mutiauk/internal/daemon"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Daemon management commands",
	}

	cmd.AddCommand(newDaemonStartCmd())
	cmd.AddCommand(newDaemonStopCmd())
	cmd.AddCommand(newDaemonReloadCmd())
	cmd.AddCommand(newDaemonStatusCmd())

	return cmd
}

func newDaemonStartCmd() *cobra.Command {
	var foreground bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := GetLogger()

			// Load configuration
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			log.Info("starting mutiauk daemon",
				zap.String("config", cfgFile),
				zap.String("tun", cfg.TUN.Name),
				zap.String("socks5", cfg.SOCKS5.Server),
			)

			// Create daemon server
			srv, err := daemon.New(cfg, cfgFile, log)
			if err != nil {
				return fmt.Errorf("failed to create daemon: %w", err)
			}

			// Set up signal handling
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

			go func() {
				for sig := range sigCh {
					switch sig {
					case syscall.SIGHUP:
						log.Info("received SIGHUP, reloading configuration")
						newCfg, err := config.Load(cfgFile)
						if err != nil {
							log.Error("failed to reload config", zap.Error(err))
							continue
						}
						if err := srv.Reload(newCfg); err != nil {
							log.Error("failed to apply new config", zap.Error(err))
						}
					case syscall.SIGINT, syscall.SIGTERM:
						log.Info("received shutdown signal", zap.String("signal", sig.String()))
						cancel()
						return
					}
				}
			}()

			// Start the daemon
			if err := srv.Run(ctx); err != nil && err != context.Canceled {
				return fmt.Errorf("daemon error: %w", err)
			}

			log.Info("daemon stopped")
			return nil
		},
	}

	cmd.Flags().BoolVarP(&foreground, "foreground", "f", true, "run in foreground")

	return cmd
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			pid, err := daemon.ReadPIDFile(cfg.Daemon.PIDFile)
			if err != nil {
				return fmt.Errorf("daemon not running or PID file not found: %w", err)
			}

			process, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("failed to find process: %w", err)
			}

			if err := process.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("failed to send SIGTERM: %w", err)
			}

			fmt.Printf("Sent SIGTERM to daemon (PID %d)\n", pid)
			return nil
		},
	}
}

func newDaemonReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Reload daemon configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			pid, err := daemon.ReadPIDFile(cfg.Daemon.PIDFile)
			if err != nil {
				return fmt.Errorf("daemon not running or PID file not found: %w", err)
			}

			process, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("failed to find process: %w", err)
			}

			if err := process.Signal(syscall.SIGHUP); err != nil {
				return fmt.Errorf("failed to send SIGHUP: %w", err)
			}

			fmt.Printf("Sent SIGHUP to daemon (PID %d)\n", pid)
			return nil
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			pid, err := daemon.ReadPIDFile(cfg.Daemon.PIDFile)
			if err != nil {
				fmt.Println("Daemon is not running")
				return nil
			}

			// Check if process is actually running
			process, err := os.FindProcess(pid)
			if err != nil {
				fmt.Println("Daemon is not running (stale PID file)")
				return nil
			}

			// On Unix, FindProcess always succeeds, so we need to send signal 0
			if err := process.Signal(syscall.Signal(0)); err != nil {
				fmt.Println("Daemon is not running (stale PID file)")
				return nil
			}

			fmt.Printf("Daemon is running (PID %d)\n", pid)
			return nil
		},
	}
}
