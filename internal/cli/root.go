package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	cfgFile string
	verbose bool
	logger  *zap.Logger
)

var rootCmd = &cobra.Command{
	Use:   "mutiauk",
	Short: "TUN to SOCKS5 proxy agent",
	Long: `Mutiauk is a TUN-based traffic capture and forwarder for TCP + UDP
into a SOCKS5 proxy, with built-in route management.

It can run in two modes:
  - Daemon mode: Creates a TUN interface and forwards traffic through SOCKS5
  - CLI mode: Manages route rules and configuration`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		if verbose {
			logger, err = zap.NewDevelopment()
		} else {
			logger, err = zap.NewProduction()
		}
		if err != nil {
			return fmt.Errorf("failed to initialize logger: %w", err)
		}
		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if logger != nil {
			_ = logger.Sync()
		}
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "/etc/mutiauk/config.yaml", "config file path")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	rootCmd.AddCommand(newDaemonCmd())
	rootCmd.AddCommand(newRouteCmd())
	rootCmd.AddCommand(newVersionCmd())
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

// GetLogger returns the configured logger
func GetLogger() *zap.Logger {
	if logger == nil {
		logger, _ = zap.NewProduction()
	}
	return logger
}

// GetConfigFile returns the config file path
func GetConfigFile() string {
	return cfgFile
}

func exitWithError(msg string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", msg, err)
	os.Exit(1)
}
