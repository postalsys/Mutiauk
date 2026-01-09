package autoroutes

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// DashboardRoute represents a route from the Muti Metroo API
type DashboardRoute struct {
	Network     string   `json:"network"`
	RouteType   string   `json:"route_type"` // "cidr" or "domain"
	Origin      string   `json:"origin"`
	OriginID    string   `json:"origin_id"`
	HopCount    int      `json:"hop_count"`
	PathDisplay []string `json:"path_display"`
	PathIDs     []string `json:"path_ids"`
}

// DashboardResponse mirrors the Muti Metroo /api/dashboard response
type DashboardResponse struct {
	Routes       []DashboardRoute `json:"routes"`
	DomainRoutes []interface{}    `json:"domain_routes,omitempty"` // Ignored
}

// Client fetches routes from Muti Metroo API
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewClient creates a new autoroutes client
func NewClient(baseURL string, timeout time.Duration, logger *zap.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
	}
}

// FetchDashboard fetches the dashboard response from the API
func (c *Client) FetchDashboard(ctx context.Context) (*DashboardResponse, error) {
	url := c.baseURL + "/api/dashboard"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dashboard: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dashboard API returned status %d", resp.StatusCode)
	}

	var dashboard DashboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&dashboard); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &dashboard, nil
}

// PathResult contains mesh path information for a route
type PathResult struct {
	Network     string
	Origin      string
	OriginID    string
	PathDisplay []string
	PathIDs     []string
	HopCount    int
}

// LookupPath finds the mesh path for an IP using longest-prefix match
func (c *Client) LookupPath(ctx context.Context, ip net.IP) (*PathResult, error) {
	dashboard, err := c.FetchDashboard(ctx)
	if err != nil {
		return nil, err
	}

	var bestMatch *DashboardRoute
	var bestPrefixLen int = -1

	for i := range dashboard.Routes {
		r := &dashboard.Routes[i]
		if r.RouteType != "cidr" {
			continue
		}

		_, ipNet, err := net.ParseCIDR(r.Network)
		if err != nil {
			continue
		}

		if !ipNet.Contains(ip) {
			continue
		}

		ones, _ := ipNet.Mask.Size()
		if ones > bestPrefixLen {
			bestPrefixLen = ones
			bestMatch = r
		}
	}

	if bestMatch == nil {
		return nil, nil // No matching route in mesh
	}

	return &PathResult{
		Network:     bestMatch.Network,
		Origin:      bestMatch.Origin,
		OriginID:    bestMatch.OriginID,
		PathDisplay: bestMatch.PathDisplay,
		PathIDs:     bestMatch.PathIDs,
		HopCount:    bestMatch.HopCount,
	}, nil
}
