package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"

	"go.uber.org/zap"
)

// Handler processes a single JSON-RPC request. The returned value is JSON-
// marshalled as the response result. Returning a *Error sends a structured
// error response; any other error becomes an InternalError.
type Handler func(ctx context.Context, params json.RawMessage) (interface{}, error)

// Server is a JSON-RPC 2.0 server speaking newline-delimited JSON over a
// net.Listener (typically a Unix socket).
type Server struct {
	log           *zap.Logger
	daemonVersion string

	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewServer constructs a server. daemonVersion is reported in the hello frame.
func NewServer(log *zap.Logger, daemonVersion string) *Server {
	return &Server{
		log:           log,
		daemonVersion: daemonVersion,
		handlers:      make(map[string]Handler),
	}
}

// Register associates a method name with a handler. Replaces any existing
// handler. Safe to call before or during Serve (the handler map is
// RWMutex-guarded).
func (s *Server) Register(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// Serve accepts connections from l until ctx is canceled or l fails. Each
// connection is served in its own goroutine. Serve returns nil when ctx is
// canceled cleanly.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	var wg sync.WaitGroup

	// done lets the watcher exit when Serve returns for non-cancellation
	// reasons (e.g., a fatal Accept error), preventing a goroutine leak.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-done:
		}
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			wg.Wait()
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			s.handleConn(ctx, c)
		}(conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	connCtx, cancelConn := context.WithCancel(ctx)
	defer cancelConn()

	var writeMu sync.Mutex
	enc := json.NewEncoder(conn)
	c := newConn(enc, &writeMu, connCtx.Done())
	handlerCtx := withConn(connCtx, c)

	if err := s.sendHelloLocked(enc, &writeMu); err != nil {
		s.log.Debug("send hello", zap.Error(err))
		return
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20) // up to 1 MB messages

	for scanner.Scan() {
		var req Message
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			s.log.Debug("malformed message", zap.Error(err))
			continue
		}
		if !req.IsRequest() && !req.IsNotification() {
			continue
		}

		s.mu.RLock()
		h, ok := s.handlers[req.Method]
		s.mu.RUnlock()

		if !ok {
			if req.IsRequest() {
				s.respondErrLocked(enc, &writeMu, req.ID, NewError(ErrCodeMethodNotFound,
					fmt.Sprintf("method %q not found", req.Method)))
			}
			continue
		}

		result, err := h(handlerCtx, req.Params)
		if !req.IsRequest() {
			if err != nil {
				s.log.Debug("notification handler error",
					zap.String("method", req.Method),
					zap.Error(err))
			}
			continue // notification: no response, even on error
		}
		if err != nil {
			var rpcErr *Error
			if errors.As(err, &rpcErr) {
				s.respondErrLocked(enc, &writeMu, req.ID, rpcErr)
			} else {
				s.respondErrLocked(enc, &writeMu, req.ID, NewError(ErrCodeInternalError, err.Error()))
			}
			continue
		}
		s.respondOKLocked(enc, &writeMu, req.ID, result)
	}
}

func (s *Server) sendHelloLocked(enc *json.Encoder, writeMu *sync.Mutex) error {
	params, err := json.Marshal(HelloParams{
		Version:       ProtocolVersion,
		DaemonVersion: s.daemonVersion,
	})
	if err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	return enc.Encode(&Message{
		JSONRPC: JSONRPCVersion,
		Method:  "hello",
		Params:  params,
	})
}

func (s *Server) respondOKLocked(enc *json.Encoder, writeMu *sync.Mutex, id *int64, result interface{}) {
	raw, err := json.Marshal(result)
	if err != nil {
		s.respondErrLocked(enc, writeMu, id, NewError(ErrCodeInternalError, "marshalling result: "+err.Error()))
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	_ = enc.Encode(&Message{JSONRPC: JSONRPCVersion, ID: id, Result: raw})
}

func (s *Server) respondErrLocked(enc *json.Encoder, writeMu *sync.Mutex, id *int64, e *Error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	_ = enc.Encode(&Message{JSONRPC: JSONRPCVersion, ID: id, Error: e})
}
