package api

import "encoding/json"

// Request represents a JSON-RPC style request
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     int             `json:"id"`
}

// Response represents a JSON-RPC style response
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
	ID     int             `json:"id"`
}

// Error represents an API error
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error codes
const (
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
)

// StatusResult contains daemon status information
type StatusResult struct {
	Running      bool              `json:"running"`
	PID          int               `json:"pid"`
	ConfigPath   string            `json:"config_path"`
	Uptime       string            `json:"uptime"`
	TUNName      string            `json:"tun_name"`
	TUNAddress   string            `json:"tun_address"`
	SOCKS5Server string            `json:"socks5_server"`
	SOCKS5Status string            `json:"socks5_status"`
	AutoRoutes   AutoRoutesStatus  `json:"autoroutes"`
	RouteCount   RouteCountStatus  `json:"route_count"`
}

// AutoRoutesStatus contains autoroutes status
type AutoRoutesStatus struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url,omitempty"`
	Count    int    `json:"count"`
	LastPoll string `json:"last_poll,omitempty"`
}

// RouteCountStatus contains route counts by source
type RouteCountStatus struct {
	Config int `json:"config"`
	Auto   int `json:"auto"`
	Manual int `json:"manual"`
	Total  int `json:"total"`
}

// RouteInfo represents a route with source information
type RouteInfo struct {
	Destination string `json:"destination"`
	Interface   string `json:"interface"`
	Gateway     string `json:"gateway,omitempty"`
	Comment     string `json:"comment,omitempty"`
	Source      string `json:"source"` // "config", "auto", "manual"
	Enabled     bool   `json:"enabled"`
}

// RouteAddParams contains parameters for route.add
type RouteAddParams struct {
	Destination string `json:"destination"`
	Comment     string `json:"comment,omitempty"`
	Persist     bool   `json:"persist"`
}

// RouteRemoveParams contains parameters for route.remove
type RouteRemoveParams struct {
	Destination string `json:"destination"`
	Persist     bool   `json:"persist"`
}

// RouteTraceParams contains parameters for route.trace
type RouteTraceParams struct {
	Destination string `json:"destination"`
}

// RouteTraceResult contains trace results
type RouteTraceResult struct {
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

// RouteListResult contains route list
type RouteListResult struct {
	Routes []RouteInfo `json:"routes"`
}

// RouteAddResult contains add result
type RouteAddResult struct {
	Success   bool   `json:"success"`
	Persisted bool   `json:"persisted"`
	Message   string `json:"message,omitempty"`
}

// RouteRemoveResult contains remove result
type RouteRemoveResult struct {
	Success   bool   `json:"success"`
	Persisted bool   `json:"persisted"`
	Message   string `json:"message,omitempty"`
}
