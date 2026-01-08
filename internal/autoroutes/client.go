package autoroutes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// DashboardRoute represents a route from the Muti Metroo API
type DashboardRoute struct {
	Network   string `json:"network"`
	RouteType string `json:"route_type"` // "cidr" or "domain"
	Origin    string `json:"origin"`
	OriginID  string `json:"origin_id"`
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
