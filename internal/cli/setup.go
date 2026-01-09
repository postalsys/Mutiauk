package cli

import (
	"fmt"

	"github.com/postalsys/mutiauk/internal/wizard"
	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup wizard",
		Long: `Launch an interactive setup wizard to configure Mutiauk.

The wizard will guide you through:
  1. Choosing a configuration file location
  2. Configuring the TUN interface (name, address, MTU)
  3. Setting up the SOCKS5 proxy connection
  4. Configuring autoroutes from Muti Metroo dashboard (optional)
  5. Defining manual routes if not using autoroutes
  6. Optionally installing Mutiauk as a system service

Example:
  sudo mutiauk setup`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := wizard.New()
			configPath, err := w.Run()
			if err != nil {
				return fmt.Errorf("setup failed: %w", err)
			}

			fmt.Println()
			fmt.Printf("Setup complete! Configuration saved to: %s\n", configPath)
			return nil
		},
	}

	return cmd
}
