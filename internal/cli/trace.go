package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/postalsys/mutiauk/internal/autoroutes"
	"github.com/postalsys/mutiauk/internal/config"
	"github.com/postalsys/mutiauk/internal/route"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// TraceResult contains the result of a route trace
type TraceResult struct {
	Destination  string   `json:"destination"`
	ResolvedIP   string   `json:"resolved_ip"`
	MatchedRoute string   `json:"matched_route"`
	Interface    string   `json:"interface"`
	Gateway      string   `json:"gateway,omitempty"`
	IsMutiauk    bool     `json:"is_mutiauk"`
	MeshPath     []string `json:"mesh_path,omitempty"`
	Origin       string   `json:"origin,omitempty"`
	OriginID     string   `json:"origin_id,omitempty"`
	HopCount     int      `json:"hop_count,omitempty"`
}

func newRouteTraceCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "trace <ip|domain>",
		Short: "Analyze routing for a destination",
		Long: `Trace the route path for an IP address or domain name.

Shows which interface handles the traffic and, if routed through Mutiauk,
displays the mesh path through Muti Metroo agents.

Examples:
  mutiauk route trace 10.10.5.100
  mutiauk route trace internal.corp.local
  mutiauk route trace 8.8.8.8 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			destination := args[0]

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			logger := GetLogger()

			// Lookup kernel route
			lookup, err := route.ResolveAndLookup(destination, cfg.TUN.Name)
			if err != nil {
				return err
			}

			result := &TraceResult{
				Destination:  destination,
				ResolvedIP:   lookup.ResolvedIP.String(),
				MatchedRoute: lookup.MatchedRoute.String(),
				Interface:    lookup.Interface,
				IsMutiauk:    lookup.IsMutiauk,
			}

			if lookup.Gateway != nil && !lookup.Gateway.IsUnspecified() {
				result.Gateway = lookup.Gateway.String()
			}

			// If routed through Mutiauk, get mesh path
			if lookup.IsMutiauk && cfg.AutoRoutes.URL != "" {
				client := autoroutes.NewClient(
					cfg.AutoRoutes.URL,
					cfg.AutoRoutes.Timeout,
					logger,
				)

				path, err := client.LookupPath(cmd.Context(), lookup.ResolvedIP)
				if err != nil {
					logger.Warn("failed to lookup mesh path", zap.Error(err))
				} else if path != nil {
					result.MeshPath = path.PathDisplay
					result.Origin = path.Origin
					result.OriginID = path.OriginID
					result.HopCount = path.HopCount
				}
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			// Human-readable output
			printTraceResult(result)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}

func printTraceResult(r *TraceResult) {
	fmt.Printf("Destination:  %s\n", r.Destination)
	if r.Destination != r.ResolvedIP {
		fmt.Printf("Resolved:     %s\n", r.ResolvedIP)
	}
	fmt.Printf("Match:        %s", r.MatchedRoute)
	if r.Gateway != "" {
		fmt.Printf(" via %s", r.Gateway)
	}
	fmt.Println()
	fmt.Printf("Interface:    %s", r.Interface)
	if r.IsMutiauk {
		fmt.Print(" (Mutiauk)")
	}
	fmt.Println()

	if r.IsMutiauk && len(r.MeshPath) > 0 {
		fmt.Printf("Mesh Path:    %s\n", strings.Join(r.MeshPath, " -> "))
		fmt.Printf("Origin:       %s [%s]\n", r.Origin, r.OriginID)
		fmt.Printf("Hop Count:    %d\n", r.HopCount)
	} else if r.IsMutiauk && len(r.MeshPath) == 0 {
		fmt.Println("Mesh Path:    (unable to determine - check autoroutes.url config)")
	} else if !r.IsMutiauk {
		fmt.Println("Note:         Not routed through Mutiauk")
	}
}
