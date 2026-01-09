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

	cmd := &cobra.Command{
		Use:   "add <cidr>",
		Short: "Add a route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cidr := args[0]

			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return fmt.Errorf("invalid CIDR: %w", err)
			}

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			mgr, err := route.NewManager(cfg.TUN.Name, GetLogger())
			if err != nil {
				return fmt.Errorf("failed to create route manager: %w", err)
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
			return nil
		},
	}

	cmd.Flags().StringVar(&comment, "comment", "", "comment for the route")

	return cmd
}

func newRouteRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <cidr>",
		Short: "Remove a route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cidr := args[0]

			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return fmt.Errorf("invalid CIDR: %w", err)
			}

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			mgr, err := route.NewManager(cfg.TUN.Name, GetLogger())
			if err != nil {
				return fmt.Errorf("failed to create route manager: %w", err)
			}

			r := route.Route{
				Destination: ipNet,
			}

			if err := mgr.Remove(r); err != nil {
				return fmt.Errorf("failed to remove route: %w", err)
			}

			fmt.Printf("Removed route: %s\n", ipNet.String())
			return nil
		},
	}
}

func newRouteListCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List managed routes",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			mgr, err := route.NewManager(cfg.TUN.Name, GetLogger())
			if err != nil {
				return fmt.Errorf("failed to create route manager: %w", err)
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
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			mgr, err := route.NewManager(cfg.TUN.Name, GetLogger())
			if err != nil {
				return fmt.Errorf("failed to create route manager: %w", err)
			}

			// Convert config routes to route.Route slice
			var desired []route.Route
			for _, r := range cfg.Routes {
				if !r.Enabled {
					continue
				}
				_, ipNet, err := net.ParseCIDR(r.Destination)
				if err != nil {
					return fmt.Errorf("invalid route in config: %s: %w", r.Destination, err)
				}
				desired = append(desired, route.Route{
					Destination: ipNet,
					Interface:   cfg.TUN.Name,
					Comment:     r.Comment,
					Enabled:     r.Enabled,
				})
			}

			plan, err := mgr.Diff(desired)
			if err != nil {
				return fmt.Errorf("failed to generate plan: %w", err)
			}

			if len(plan.ToAdd) == 0 && len(plan.ToRemove) == 0 {
				fmt.Println("No changes required")
				return nil
			}

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

			return nil
		},
	}
}

func newRouteApplyCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply routes from configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			mgr, err := route.NewManager(cfg.TUN.Name, GetLogger())
			if err != nil {
				return fmt.Errorf("failed to create route manager: %w", err)
			}

			// Convert config routes to route.Route slice
			var desired []route.Route
			for _, r := range cfg.Routes {
				if !r.Enabled {
					continue
				}
				_, ipNet, err := net.ParseCIDR(r.Destination)
				if err != nil {
					return fmt.Errorf("invalid route in config: %s: %w", r.Destination, err)
				}
				desired = append(desired, route.Route{
					Destination: ipNet,
					Interface:   cfg.TUN.Name,
					Comment:     r.Comment,
					Enabled:     r.Enabled,
				})
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
				for _, r := range plan.ToAdd {
					fmt.Printf("  + %s\n", r.Destination.String())
				}
				for _, r := range plan.ToRemove {
					fmt.Printf("  - %s\n", r.Destination.String())
				}
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
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			mgr, err := route.NewManager(cfg.TUN.Name, GetLogger())
			if err != nil {
				return fmt.Errorf("failed to create route manager: %w", err)
			}

			// Convert config routes to route.Route slice
			var routes []route.Route
			for _, r := range cfg.Routes {
				_, ipNet, err := net.ParseCIDR(r.Destination)
				if err != nil {
					return fmt.Errorf("invalid route in config: %s: %w", r.Destination, err)
				}
				routes = append(routes, route.Route{
					Destination: ipNet,
					Comment:     r.Comment,
					Enabled:     r.Enabled,
				})
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
