package api

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Client is a Unix socket API client
type Client struct {
	socketPath string
	timeout    time.Duration
}

// NewClient creates a new API client
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		timeout:    5 * time.Second,
	}
}

// SetTimeout sets the connection timeout
func (c *Client) SetTimeout(d time.Duration) {
	c.timeout = d
}

// IsRunning checks if the daemon is running by attempting to connect
func (c *Client) IsRunning() bool {
	conn, err := net.DialTimeout("unix", c.socketPath, c.timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Call makes an API call to the daemon
func (c *Client) Call(method string, params any) (*Response, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer conn.Close()

	// Set read/write deadline
	conn.SetDeadline(time.Now().Add(c.timeout))

	// Build request
	req := Request{Method: method, ID: 1}
	if params != nil {
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal params: %w", err)
		}
		req.Params = paramsJSON
	}

	// Send request
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	var resp Response
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return &resp, nil
}

// Status gets the daemon status
func (c *Client) Status() (*StatusResult, error) {
	resp, err := c.Call("status", nil)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("API error: %s", resp.Error.Message)
	}

	var result StatusResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal status: %w", err)
	}

	return &result, nil
}

// RouteList gets the list of active routes
func (c *Client) RouteList() (*RouteListResult, error) {
	resp, err := c.Call("route.list", nil)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("API error: %s", resp.Error.Message)
	}

	var result RouteListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal routes: %w", err)
	}

	return &result, nil
}

// RouteAdd adds a route
func (c *Client) RouteAdd(destination, comment string, persist bool) (*RouteAddResult, error) {
	params := RouteAddParams{
		Destination: destination,
		Comment:     comment,
		Persist:     persist,
	}

	resp, err := c.Call("route.add", params)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("API error: %s", resp.Error.Message)
	}

	var result RouteAddResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return &result, nil
}

// RouteRemove removes a route
func (c *Client) RouteRemove(destination string, persist bool) (*RouteRemoveResult, error) {
	params := RouteRemoveParams{
		Destination: destination,
		Persist:     persist,
	}

	resp, err := c.Call("route.remove", params)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("API error: %s", resp.Error.Message)
	}

	var result RouteRemoveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return &result, nil
}

// RouteTrace traces routing for a destination
func (c *Client) RouteTrace(destination string) (*RouteTraceResult, error) {
	params := RouteTraceParams{
		Destination: destination,
	}

	resp, err := c.Call("route.trace", params)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("API error: %s", resp.Error.Message)
	}

	var result RouteTraceResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return &result, nil
}

// ConfigPath gets the config file path the daemon was started with
func (c *Client) ConfigPath() (string, error) {
	resp, err := c.Call("config.path", nil)
	if err != nil {
		return "", err
	}

	if resp.Error != nil {
		return "", fmt.Errorf("API error: %s", resp.Error.Message)
	}

	var result map[string]string
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return result["path"], nil
}
