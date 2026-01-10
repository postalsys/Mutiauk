package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"text/tabwriter"

	"github.com/postalsys/mutiauk/internal/config"
	"github.com/postalsys/mutiauk/internal/route"
	"github.com/spf13/cobra"
)

func newRouteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "route",
		Short: "Route management commands",
	}

	cmd.AddCommand(newRouteAddCmd())
	cmd.AddCommand(newRouteRemoveCmd())
	cmd.AddCommand(newRouteListCmd())
	cmd.AddCommand(newRoutePlanCmd())
	cmd.AddCommand(newRouteApplyCmd())
	cmd.AddCommand(newRouteCheckCmd())
	cmd.AddCommand(newRouteTraceCmd())

	return cmd
}

func newRouteAddCmd() *cobra.Command {
	var comment string
	var persist bool

	cmd := &cobra.Command{
		Use:   "add <cidr>",
		Short: "Add a route",
		Long: `Add a route to the kernel routing table.

By default, routes are added to the kernel only and will be lost on reboot.
Use --persist to also save the route to the config file.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cidr := args[0]

			_, _, err := net.ParseCIDR(cidr)
			if err != nil {
				return fmt.Errorf("invalid CIDR: %w", err)
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			// Try daemon API first
			daemonClient := newDaemonAPIClient(cfg)
			if result, err := daemonClient.AddRoute(cidr, comment, persist); err != nil {
				return err
			} else if result != nil {
				fmt.Printf("Added route: %s\n", cidr)
				if result.Persisted {
					fmt.Printf("Saved to config: %s\n", cfgFile)
				}
				return nil
			}

			// Fallback to direct kernel manipulation
			return addRouteDirect(cfg, cidr, comment, persist)
		},
	}

	cmd.Flags().StringVar(&comment, "comment", "", "comment for the route")
	cmd.Flags().BoolVar(&persist, "persist", false, "save route to config file")

	return cmd
}

func addRouteDirect(cfg *config.Config, cidr, comment string, persist bool) error {
	_, ipNet, _ := net.ParseCIDR(cidr)

	mgr, err := newRouteManager(cfg)
	if err != nil {
		return err
	}

	r := route.Route{
		Destination: ipNet,
		Comment:     comment,
		Enabled:     true,
	}

	if err := mgr.Add(r); err != nil {
		return fmt.Errorf("failed to add route: %w", err)
	}

	fmt.Printf("Added route: %s\n", ipNet.String())

	if persist {
		cfg.Routes = append(cfg.Routes, config.RouteConfig{
			Destination: cidr,
			Comment:     comment,
			Enabled:     true,
		})
		if err := cfg.Save(cfgFile); err != nil {
			return fmt.Errorf("route added but failed to save config: %w", err)
		}
		fmt.Printf("Saved to config: %s\n", cfgFile)
	}

	return nil
}

func newRouteRemoveCmd() *cobra.Command {
	var persist bool

	cmd := &cobra.Command{
		Use:   "remove <cidr>",
		Short: "Remove a route",
		Long: `Remove a route from the kernel routing table.

Use --persist to also remove the route from the config file.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cidr := args[0]

			_, _, err := net.ParseCIDR(cidr)
			if err != nil {
				return fmt.Errorf("invalid CIDR: %w", err)
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			// Try daemon API first
			daemonClient := newDaemonAPIClient(cfg)
			if result, err := daemonClient.RemoveRoute(cidr, persist); err != nil {
				return err
			} else if result != nil {
				fmt.Printf("Removed route: %s\n", cidr)
				if result.Persisted {
					fmt.Printf("Removed from config: %s\n", cfgFile)
				}
				return nil
			}

			// Fallback to direct kernel manipulation
			return removeRouteDirect(cfg, cidr, persist)
		},
	}

	cmd.Flags().BoolVar(&persist, "persist", false, "remove route from config file")

	return cmd
}

func removeRouteDirect(cfg *config.Config, cidr string, persist bool) error {
	_, ipNet, _ := net.ParseCIDR(cidr)

	mgr, err := newRouteManager(cfg)
	if err != nil {
		return err
	}

	r := route.Route{
		Destination: ipNet,
	}

	if err := mgr.Remove(r); err != nil {
		return fmt.Errorf("failed to remove route: %w", err)
	}

	fmt.Printf("Removed route: %s\n", ipNet.String())

	if persist {
		for i, rc := range cfg.Routes {
			if rc.Destination == cidr {
				cfg.Routes = append(cfg.Routes[:i], cfg.Routes[i+1:]...)
				break
			}
		}
		if err := cfg.Save(cfgFile); err != nil {
			return fmt.Errorf("route removed but failed to save config: %w", err)
		}
		fmt.Printf("Removed from config: %s\n", cfgFile)
	}

	return nil
}

func newRouteListCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List managed routes",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			mgr, err := newRouteManager(cfg)
			if err != nil {
				return err
			}

			routes, err := mgr.List()
			if err != nil {
				return fmt.Errorf("failed to list routes: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(routes)
			}

			if len(routes) == 0 {
				fmt.Println("No routes configured")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "DESTINATION\tINTERFACE\tCOMMENT")
			for _, r := range routes {
				fmt.Fprintf(w, "%s\t%s\t%s\n", r.Destination.String(), r.Interface, r.Comment)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output in JSON format")

	return cmd
}

func newRoutePlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Show pending route changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			mgr, err := newRouteManager(cfg)
			if err != nil {
				return err
			}

			desired, err := configRoutesToRoutes(cfg)
			if err != nil {
				return err
			}

			plan, err := mgr.Diff(desired)
			if err != nil {
				return fmt.Errorf("failed to generate plan: %w", err)
			}

			if len(plan.ToAdd) == 0 && len(plan.ToRemove) == 0 {
				fmt.Println("No changes required")
				return nil
			}

			printRoutePlan(plan)
			return nil
		},
	}
}

func printRoutePlan(plan *route.Plan) {
	if len(plan.ToAdd) > 0 {
		fmt.Println("Routes to add:")
		for _, r := range plan.ToAdd {
			fmt.Printf("  + %s\n", r.Destination.String())
		}
	}

	if len(plan.ToRemove) > 0 {
		fmt.Println("Routes to remove:")
		for _, r := range plan.ToRemove {
			fmt.Printf("  - %s\n", r.Destination.String())
		}
	}
}

func newRouteApplyCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply routes from configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			mgr, err := newRouteManager(cfg)
			if err != nil {
				return err
			}

			desired, err := configRoutesToRoutes(cfg)
			if err != nil {
				return err
			}

			plan, err := mgr.Diff(desired)
			if err != nil {
				return fmt.Errorf("failed to generate plan: %w", err)
			}

			if len(plan.ToAdd) == 0 && len(plan.ToRemove) == 0 {
				fmt.Println("No changes required")
				return nil
			}

			if dryRun {
				fmt.Println("Dry run - would apply:")
				printRoutePlan(plan)
				return nil
			}

			if err := mgr.Apply(plan); err != nil {
				return fmt.Errorf("failed to apply routes: %w", err)
			}

			fmt.Printf("Applied %d additions, %d removals\n", len(plan.ToAdd), len(plan.ToRemove))
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without making changes")

	return cmd
}

func newRouteCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check for route conflicts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			mgr, err := newRouteManager(cfg)
			if err != nil {
				return err
			}

			routes, err := configRoutesToRoutesAll(cfg)
			if err != nil {
				return err
			}

			conflicts, err := mgr.DetectConflicts(routes)
			if err != nil {
				return fmt.Errorf("failed to check conflicts: %w", err)
			}

			if len(conflicts) == 0 {
				fmt.Println("No conflicts detected")
				return nil
			}

			fmt.Printf("Found %d conflict(s):\n", len(conflicts))
			for _, c := range conflicts {
				fmt.Printf("  - %s conflicts with %s: %s\n",
					c.Proposed.Destination.String(),
					c.Existing.Destination.String(),
					c.Reason,
				)
			}

			return nil
		},
	}
}
