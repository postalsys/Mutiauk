package autoroutes

import (
	"context"
	"sync"
	"time"

	"github.com/postalsys/mutiauk/internal/route"
	"go.uber.org/zap"
)

// UpdateCallback is called when new routes are fetched
type UpdateCallback func(routes []route.Route)

// Poller periodically fetches routes from the Muti Metroo API
type Poller struct {
	client   *Client
	interval time.Duration
	tunName  string
	logger   *zap.Logger

	mu         sync.RWMutex
	lastRoutes []route.Route
	lastError  error
}

// NewPoller creates a new autoroutes poller
func NewPoller(client *Client, interval time.Duration, tunName string, logger *zap.Logger) *Poller {
	return &Poller{
		client:   client,
		interval: interval,
		tunName:  tunName,
		logger:   logger,
	}
}

// Start begins the polling loop. It performs an initial fetch immediately,
// then polls at the configured interval. The onUpdate callback is called
// whenever routes are successfully fetched (even if empty).
// The function blocks until the context is cancelled.
func (p *Poller) Start(ctx context.Context, onUpdate UpdateCallback) {
	// Initial fetch
	p.fetchAndUpdate(ctx, onUpdate)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Debug("autoroutes poller stopped")
			return
		case <-ticker.C:
			p.fetchAndUpdate(ctx, onUpdate)
		}
	}
}

// fetchAndUpdate fetches routes and calls the update callback
func (p *Poller) fetchAndUpdate(ctx context.Context, onUpdate UpdateCallback) {
	routes, err := p.fetch(ctx)
	if err != nil {
		p.mu.Lock()
		p.lastError = err
		previousCount := len(p.lastRoutes)
		p.mu.Unlock()

		p.logger.Warn("failed to fetch autoroutes",
			zap.Error(err),
			zap.Int("keeping_previous", previousCount),
		)
		// Keep previous routes on error
		return
	}

	p.mu.Lock()
	p.lastRoutes = routes
	p.lastError = nil
	p.mu.Unlock()

	p.logger.Debug("fetched autoroutes",
		zap.Int("count", len(routes)),
	)

	if onUpdate != nil {
		onUpdate(routes)
	}
}

// fetch fetches and processes routes from the API
func (p *Poller) fetch(ctx context.Context) ([]route.Route, error) {
	dashboard, err := p.client.FetchDashboard(ctx)
	if err != nil {
		return nil, err
	}

	// Filter routes (exclude domain routes, default routes, invalid CIDR)
	autoRoutes := FilterRoutes(dashboard.Routes, p.tunName)

	// Resolve conflicts (deduplicate by network)
	resolved := ResolveConflicts(autoRoutes)

	return resolved, nil
}

// GetLastRoutes returns the most recently fetched routes
func (p *Poller) GetLastRoutes() []route.Route {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.lastRoutes == nil {
		return nil
	}

	// Return a copy to prevent mutation
	result := make([]route.Route, len(p.lastRoutes))
	copy(result, p.lastRoutes)
	return result
}

// GetLastError returns the last fetch error, if any
func (p *Poller) GetLastError() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastError
}
