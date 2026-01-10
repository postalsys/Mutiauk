// Package wizard provides an interactive setup wizard for Mutiauk.
package wizard

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const (
	dashboardAPIPath    = "/api/dashboard"
	dashboardAPITimeout = 10 * time.Second
)

// DashboardVerifier handles verification of dashboard API connectivity.
type DashboardVerifier struct {
	client *http.Client
}

// NewDashboardVerifier creates a new dashboard verifier with default settings.
func NewDashboardVerifier() *DashboardVerifier {
	return &DashboardVerifier{
		client: &http.Client{Timeout: dashboardAPITimeout},
	}
}

// Verify checks if the dashboard API is accessible at the given base URL.
// Returns nil if the API is accessible, or an error describing the failure.
func (d *DashboardVerifier) Verify(baseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dashboardAPITimeout)
	defer cancel()

	apiURL := baseURL + dashboardAPIPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// DashboardConnectionError describes common causes for dashboard connection failures.
type DashboardConnectionError struct {
	BaseURL string
	Err     error
}

// Error implements the error interface.
func (e *DashboardConnectionError) Error() string {
	return fmt.Sprintf("failed to connect to dashboard API at %s: %v", e.BaseURL, e.Err)
}

// Unwrap returns the underlying error.
func (e *DashboardConnectionError) Unwrap() error {
	return e.Err
}

// PossibleCauses returns a list of possible reasons for the connection failure.
func (e *DashboardConnectionError) PossibleCauses() []string {
	return []string{
		"Dashboard server is not running",
		"Incorrect URL",
		"Network connectivity issues",
		"Firewall blocking the connection",
	}
}
