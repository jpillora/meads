package webui

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// withMiddleware wraps mux with token auth and DNS-rebinding defense.
// Static asset paths are served without auth (they contain no sensitive data
// and browsers can't carry the ?token= query through HTML asset links).
func withMiddleware(mux http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS preflight: allow any localhost.
		if r.Method == http.MethodOptions {
			setCORS(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		setCORS(w)

		if !originOK(r) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		if !isStaticAsset(r) && !authOK(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// isStaticAsset returns true for GET requests to non-API/non-bind paths.
// These are HTML/JS/CSS served from the embedded asset FS.
func isStaticAsset(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	if p == "/api" || strings.HasPrefix(p, "/api/") {
		return false
	}
	if p == "/bind-vscode" {
		return false
	}
	return true
}

// authOK accepts a bearer token in the Authorization header or a ?token= query param.
func authOK(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	if got := r.URL.Query().Get("token"); got != "" && subtleEqual(got, token) {
		return true
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return subtleEqual(strings.TrimPrefix(h, "Bearer "), token)
	}
	return false
}

// originOK rejects cross-origin requests from non-loopback hosts.
// Absent Origin (common for same-origin XHR and curl) is allowed.
func originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopbackHost(u.Hostname())
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// subtleEqual compares two strings in constant time.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
}
