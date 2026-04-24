package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed assets
var assetsFS embed.FS

// assetHandler returns an http.Handler serving the web UI assets.
// When Dev is true, assets are served from disk for hot-reload during
// development; otherwise they come from the embedded FS.
func (s *Server) assetHandler() http.Handler {
	if s.cfg.Dev {
		// Best-effort: look up "pkg/webui/assets" relative to CWD.
		for _, p := range []string{
			"pkg/webui/assets",
			"assets",
			filepath.Join("..", "assets"),
		} {
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				return http.FileServer(http.Dir(p))
			}
		}
	}
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		// Embed failure is a programming error — panic with a helpful message.
		panic("webui: embedded assets missing: " + err.Error())
	}
	return http.FileServer(http.FS(sub))
}
