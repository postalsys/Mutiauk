package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"go.uber.org/zap"
)

// StateProvider interface for daemon to implement
type StateProvider interface {
	GetStatus() *StatusResult
	GetRoutes() []RouteInfo
	AddRoute(dest, comment string, persist bool) error
	RemoveRoute(dest string, persist bool) error
	TraceRoute(dest string) (*RouteTraceResult, error)
	GetConfigPath() string
}

// HandlerFunc is a function that handles an API method
type HandlerFunc func(params json.RawMessage) (any, error)

// Server is the Unix socket API server
type Server struct {
	socketPath string
	listener   net.Listener
	handlers   map[string]HandlerFunc
	state      StateProvider
	mu         sync.RWMutex
	logger     *zap.Logger
	wg         sync.WaitGroup
}

// NewServer creates a new API server
func NewServer(socketPath string, state StateProvider, logger *zap.Logger) *Server {
	s := &Server{
		socketPath: socketPath,
		handlers:   make(map[string]HandlerFunc),
		state:      state,
		logger:     logger,
	}
	s.registerHandlers()
	return s
}

// registerHandlers sets up all API method handlers
func (s *Server) registerHandlers() {
	s.handlers["status"] = s.handleStatus
	s.handlers["route.list"] = s.handleRouteList
	s.handlers["route.add"] = s.handleRouteAdd
	s.handlers["route.remove"] = s.handleRouteRemove
	s.handlers["route.trace"] = s.handleRouteTrace
	s.handlers["config.path"] = s.handleConfigPath
}

// Start starts the API server
func (s *Server) Start(ctx context.Context) error {
	// Ensure parent directory exists
	dir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Remove stale socket
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	s.listener = listener

	// Set permissions (owner read/write only)
	if err := os.Chmod(s.socketPath, 0600); err != nil {
		s.logger.Warn("failed to set socket permissions", zap.Error(err))
	}

	s.logger.Info("API server started", zap.String("socket", s.socketPath))

	s.wg.Add(1)
	go s.acceptLoop(ctx)

	return nil
}

// Stop stops the API server
func (s *Server) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
	os.Remove(s.socketPath)
	s.logger.Info("API server stopped")
}

// acceptLoop accepts incoming connections
func (s *Server) acceptLoop(ctx context.Context) {
	defer s.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.logger.Debug("accept error", zap.Error(err))
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConnection(ctx, conn)
	}
}

// handleConnection handles a single client connection
func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var req Request
		if err := decoder.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			s.logger.Debug("decode error", zap.Error(err))
			return
		}

		resp := s.handleRequest(&req)
		if err := encoder.Encode(resp); err != nil {
			s.logger.Debug("encode error", zap.Error(err))
			return
		}
	}
}

// handleRequest processes a single request
func (s *Server) handleRequest(req *Request) *Response {
	s.mu.RLock()
	handler, ok := s.handlers[req.Method]
	s.mu.RUnlock()

	if !ok {
		return &Response{
			Error: &Error{
				Code:    ErrCodeMethodNotFound,
				Message: "method not found: " + req.Method,
			},
			ID: req.ID,
		}
	}

	result, err := handler(req.Params)
	if err != nil {
		return &Response{
			Error: &Error{
				Code:    ErrCodeInternal,
				Message: err.Error(),
			},
			ID: req.ID,
		}
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return &Response{
			Error: &Error{
				Code:    ErrCodeInternal,
				Message: "failed to marshal result",
			},
			ID: req.ID,
		}
	}

	return &Response{
		Result: resultJSON,
		ID:     req.ID,
	}
}

// Handler implementations

func (s *Server) handleStatus(_ json.RawMessage) (any, error) {
	return s.state.GetStatus(), nil
}

func (s *Server) handleRouteList(_ json.RawMessage) (any, error) {
	routes := s.state.GetRoutes()
	return &RouteListResult{Routes: routes}, nil
}

func (s *Server) handleRouteAdd(params json.RawMessage) (any, error) {
	var p RouteAddParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}

	if err := s.state.AddRoute(p.Destination, p.Comment, p.Persist); err != nil {
		return &RouteAddResult{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	return &RouteAddResult{
		Success:   true,
		Persisted: p.Persist,
	}, nil
}

func (s *Server) handleRouteRemove(params json.RawMessage) (any, error) {
	var p RouteRemoveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}

	if err := s.state.RemoveRoute(p.Destination, p.Persist); err != nil {
		return &RouteRemoveResult{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	return &RouteRemoveResult{
		Success:   true,
		Persisted: p.Persist,
	}, nil
}

func (s *Server) handleRouteTrace(params json.RawMessage) (any, error) {
	var p RouteTraceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}

	return s.state.TraceRoute(p.Destination)
}

func (s *Server) handleConfigPath(_ json.RawMessage) (any, error) {
	return map[string]string{"path": s.state.GetConfigPath()}, nil
}
