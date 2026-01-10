// Package wizard provides an interactive setup wizard for Mutiauk.
package wizard

import (
	"fmt"
	"runtime"

	"github.com/postalsys/mutiauk/internal/service"
	"github.com/postalsys/mutiauk/internal/wizard/prompt"
)

// ServiceInstaller handles the interactive service installation flow.
type ServiceInstaller struct {
	configPath string
}

// NewServiceInstaller creates a new service installer for the given config path.
func NewServiceInstaller(configPath string) *ServiceInstaller {
	return &ServiceInstaller{configPath: configPath}
}

// Run executes the interactive service installation flow.
// Returns nil on success or if the user declines installation.
func (s *ServiceInstaller) Run() error {
	prompt.PrintHeader("Service Installation", "Install Mutiauk as a system service.")

	if !s.isLinux() {
		prompt.PrintInfo("Service installation is only available on Linux.")
		return nil
	}

	if !s.isRoot() {
		s.printNonRootInstructions()
		return nil
	}

	return s.promptAndInstall()
}

func (s *ServiceInstaller) isLinux() bool {
	return runtime.GOOS == "linux"
}

func (s *ServiceInstaller) isRoot() bool {
	return service.IsRoot()
}

func (s *ServiceInstaller) printNonRootInstructions() {
	prompt.PrintWarning("Not running as root. Service installation requires root privileges.")
	fmt.Println()
	s.printManualInstallCommand()
}

func (s *ServiceInstaller) printManualInstallCommand() {
	fmt.Println("To install as a service later, run:")
	fmt.Printf("  sudo mutiauk service install -c %s\n", s.configPath)
}

func (s *ServiceInstaller) promptAndInstall() error {
	install, err := prompt.Confirm("Install Mutiauk as a systemd service?", true)
	if err != nil {
		return err
	}

	if !install {
		fmt.Println()
		s.printManualInstallCommand()
		return nil
	}

	if service.IsInstalled("mutiauk") {
		prompt.PrintWarning("Service is already installed. Uninstall first to reinstall.")
		return nil
	}

	return s.install()
}

func (s *ServiceInstaller) install() error {
	fmt.Println()
	prompt.PrintInfo("Installing systemd service...")

	cfg := service.DefaultConfig(s.configPath)
	if err := service.Install(cfg); err != nil {
		prompt.PrintError(fmt.Sprintf("Failed to install service: %v", err))
		fmt.Println()
		fmt.Println("You can try installing manually:")
		fmt.Printf("  sudo mutiauk service install -c %s\n", s.configPath)
		return nil // Don't fail the wizard for service install failure
	}

	prompt.PrintSuccess("Service installed and started!")
	s.printServiceManagementCommands()

	return nil
}

func (s *ServiceInstaller) printServiceManagementCommands() {
	fmt.Println()
	fmt.Println("Service management commands:")
	fmt.Println("  sudo systemctl status mutiauk    # Check status")
	fmt.Println("  sudo systemctl stop mutiauk      # Stop service")
	fmt.Println("  sudo systemctl restart mutiauk   # Restart service")
	fmt.Println("  sudo journalctl -u mutiauk -f    # View logs")
}
