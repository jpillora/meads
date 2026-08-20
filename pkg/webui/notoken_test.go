package webui

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/jpillora/meads/pkg/meads"
)

func newNoTokenStore() meads.Tasks {
	return meads.NewFileTasks(meads.NewStore(memfs.New(), "TASKS.md"), nil)
}

// TestNoToken_SkipsGeneration asserts NoToken suppresses the auto-generated
// token rather than merely hiding it - withMiddleware keys off an empty token
// to disable auth, so a generated-but-unprinted token would lock everyone out.
func TestNoToken_SkipsGeneration(t *testing.T) {
	s, err := New(Config{Store: newNoTokenStore(), NoToken: true, Print: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Token(); got != "" {
		t.Fatalf("expected empty token, got %q", got)
	}
}

func TestNoToken_ConflictsWithToken(t *testing.T) {
	_, err := New(Config{Store: newNoTokenStore(), Token: "tok", NoToken: true, Print: "none"})
	if err == nil {
		t.Fatal("expected an error when Token and NoToken are both set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNoToken_ServesUnauthenticated covers the whole point of the flag: the
// same request that yields 401 in TestAuth_Unauthorized must succeed here.
func TestNoToken_ServesUnauthenticated(t *testing.T) {
	s, err := New(Config{Store: newNoTokenStore(), NoToken: true, Print: "none"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(withMiddleware(s.routes(), s.Token()))
	t.Cleanup(func() {
		ts.Close()
		s.bind.closeAll()
		s.events.closeAll()
	})

	resp, body := do(t, ts, "", http.MethodGet, "/api/tasks", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var tasks []map[string]any
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}

func TestBrowseURL(t *testing.T) {
	const base = "http://127.0.0.1:4000"
	if got, want := BrowseURL(base, ""), base+"/"; got != want {
		t.Errorf("BrowseURL(no token) = %q, want %q", got, want)
	}
	if got, want := BrowseURL(base, "a b"), base+"/?token=a+b"; got != want {
		t.Errorf("BrowseURL(token) = %q, want %q", got, want)
	}
}

// TestPrintStartLine_NoToken checks both --print formats stay usable without a
// token: no dangling "?token=" in the url form, and an explicit empty token in
// the json form (the VS Code extension rejects that line, which is correct -
// it spawns md webui itself and never passes --no-token).
func TestPrintStartLine_NoToken(t *testing.T) {
	for _, format := range []string{"url", "json"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			s, err := New(Config{Store: newNoTokenStore(), NoToken: true, Print: format, Stdout: &out})
			if err != nil {
				t.Fatal(err)
			}
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer lis.Close()
			s.setListener(lis)

			if err := s.printStartLine(); err != nil {
				t.Fatal(err)
			}
			line := strings.TrimSpace(out.String())
			if strings.Contains(line, "token=") {
				t.Errorf("start line leaks a token query: %q", line)
			}
			switch format {
			case "url":
				if want := s.URL() + "/"; line != want {
					t.Errorf("start line = %q, want %q", line, want)
				}
			case "json":
				var info startInfo
				if err := json.Unmarshal([]byte(line), &info); err != nil {
					t.Fatalf("parse %q: %v", line, err)
				}
				if info.Token != "" {
					t.Errorf("expected empty token in start info, got %q", info.Token)
				}
				if info.URL == "" {
					t.Errorf("missing url in start info: %q", line)
				}
			}
		})
	}
}
