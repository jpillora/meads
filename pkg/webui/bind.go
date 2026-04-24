package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// ErrNoBindClient is returned when a vscode.* RPC call is attempted but no
// VS Code client is currently connected to /bind-vscode.
var ErrNoBindClient = errors.New("no VS Code client bound")

// bindHub tracks WebSocket clients that have connected to /bind-vscode.
// Clients are VS Code extensions; the server can invoke JSON-RPC methods on them.
type bindHub struct {
	mu      sync.RWMutex
	nextID  atomic.Int64
	clients map[int64]*bindClient
}

func newBindHub() *bindHub {
	return &bindHub{clients: make(map[int64]*bindClient)}
}

func (h *bindHub) add(c *bindClient) {
	h.mu.Lock()
	h.clients[c.id] = c
	h.mu.Unlock()
}

func (h *bindHub) remove(id int64) {
	h.mu.Lock()
	if c, ok := h.clients[id]; ok {
		delete(h.clients, id)
		c.close()
	}
	h.mu.Unlock()
}

func (h *bindHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, c := range h.clients {
		c.close()
		delete(h.clients, id)
	}
}

// Call invokes method on the first available bound client. Returns
// ErrNoBindClient if none are connected.
func (h *bindHub) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	h.mu.RLock()
	var c *bindClient
	for _, cl := range h.clients {
		c = cl
		break
	}
	h.mu.RUnlock()
	if c == nil {
		return nil, ErrNoBindClient
	}
	return c.call(ctx, method, params)
}

// bindClient represents a single WebSocket client subscribed to the bind channel.
// Messages are JSON-RPC 2.0 over text frames.
type bindClient struct {
	id     int64
	conn   *websocket.Conn
	hub    *bindHub

	sendMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[int64]chan rpcResponse
	nextRPC   atomic.Int64

	closeOnce sync.Once
	closed    chan struct{}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

func (s *Server) handleBindVSCode(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"}, // origin already validated in middleware
	})
	if err != nil {
		return
	}
	id := s.bind.nextID.Add(1)
	c := &bindClient{
		id:      id,
		conn:    conn,
		hub:     s.bind,
		pending: make(map[int64]chan rpcResponse),
		closed:  make(chan struct{}),
	}
	s.bind.add(c)
	defer s.bind.remove(id)

	// Announce connection to the SSE bus (so any interested UI can react).
	s.events.publish(event{Kind: "bind_connected"})
	defer s.events.publish(event{Kind: "bind_disconnected"})

	c.readLoop(r.Context())
}

func (c *bindClient) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close(websocket.StatusNormalClosure, "bye")
	})
}

func (c *bindClient) readLoop(ctx context.Context) {
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg rpcResponse
		if err := json.Unmarshal(data, &msg); err == nil && msg.ID != nil && (msg.Result != nil || msg.Error != nil) && msg.JSONRPC == "2.0" {
			c.pendingMu.Lock()
			ch, ok := c.pending[*msg.ID]
			if ok {
				delete(c.pending, *msg.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				select {
				case ch <- msg:
				default:
				}
			}
			continue
		}
		// Otherwise treat as a request from client → server (webui.* methods).
		var req rpcRequest
		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}
		c.handleRequest(ctx, req)
	}
}

func (c *bindClient) handleRequest(ctx context.Context, req rpcRequest) {
	if req.ID == nil {
		// Notification; ignore for now.
		return
	}
	// webui doesn't currently expose any methods; return "method not found".
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error: &rpcError{
			Code:    -32601,
			Message: "method not found: " + req.Method,
		},
	}
	_ = c.writeJSON(ctx, resp)
}

// call sends a JSON-RPC request and awaits the response.
func (c *bindClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextRPC.Add(1)
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  raw,
	}
	ch := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.writeJSON(ctx, req); err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case <-callCtx.Done():
		return nil, callCtx.Err()
	case <-c.closed:
		return nil, errors.New("bind client closed")
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (c *bindClient) writeJSON(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, b)
}

