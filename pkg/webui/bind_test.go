package webui

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/jpillora/meads/pkg/meads"
)

// newTestServerForBind is the same as newTestServer but returns the underlying
// *Server so tests can exercise bindHub.Call directly.
func newTestServerForBind(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	// git is nil: see handlers_test.go's newTestServer.
	store := meads.NewFileTasks(meads.NewStore(memfs.New(), "TASKS.md"), nil)
	s, err := New(Config{Store: store, Token: "tok", Print: "none"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(withMiddleware(s.routes(), s.Token()))
	t.Cleanup(func() {
		ts.Close()
		s.bind.closeAll()
		s.events.closeAll()
	})
	return ts, s
}

func TestBindVSCode_RPCRoundtrip(t *testing.T) {
	ts, s := newTestServerForBind(t)
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/bind-vscode?token=" + s.Token()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	// Give the server a moment to register the client.
	time.Sleep(100 * time.Millisecond)

	// Mock vscode.showMessage by responding to whatever the server sends.
	done := make(chan json.RawMessage, 1)
	go func() {
		_, data, err := conn.Read(ctx)
		if err != nil {
			done <- nil
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(data, &req); err != nil {
			done <- nil
			return
		}
		resp := rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`"ok"`),
		}
		out, _ := json.Marshal(resp)
		_ = conn.Write(ctx, websocket.MessageText, out)
		done <- req.Params
	}()

	// Server-side: invoke the method on the bound client.
	result, err := s.bind.Call(ctx, "vscode.showMessage", map[string]string{"level": "info", "text": "hi"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(result) != `"ok"` {
		t.Errorf("expected result=\"ok\", got %s", result)
	}

	// Assert the params we sent arrived at the client.
	select {
	case params := <-done:
		if params == nil {
			t.Fatal("client never received request")
		}
		var got map[string]string
		if err := json.Unmarshal(params, &got); err != nil {
			t.Fatalf("decode params: %v", err)
		}
		if got["level"] != "info" || got["text"] != "hi" {
			t.Errorf("params mismatch: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for client to receive request")
	}
}

func TestBindVSCode_NoClient_ReturnsError(t *testing.T) {
	_, s := newTestServerForBind(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := s.bind.Call(ctx, "vscode.showMessage", nil)
	if err != ErrNoBindClient {
		t.Errorf("expected ErrNoBindClient, got %v", err)
	}
}
