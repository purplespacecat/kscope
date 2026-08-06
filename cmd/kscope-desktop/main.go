// Command kscope-desktop is the native desktop shell around the same core the
// CLI binary uses. It opens an OS webview instead of asking you to visit a
// localhost URL, and it never opens a TCP port: the HTTP API is served
// in-process through Wails' asset server.
package main

import (
	"context"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/purplespacecat/kscope/internal/graph"
	"github.com/purplespacecat/kscope/internal/paths"
	"github.com/purplespacecat/kscope/internal/server"
	"github.com/purplespacecat/kscope/web"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

func main() {
	dataDir := flag.String("data-dir", paths.DataDir(), "directory for persisted snapshot")
	redactExtra := flag.String("redact-extra", "", "extra comma-separated dotted paths to redact in every manifest, e.g. spec.password")
	flag.Parse()

	// Must be set before any discovery runs — redaction happens at capture
	// time, never retroactively.
	for _, p := range strings.Split(*redactExtra, ",") {
		if p = strings.TrimSpace(p); p != "" {
			graph.ExtraRedactPaths = append(graph.ExtraRedactPaths, p)
		}
	}

	store := graph.NewStore(filepath.Join(*dataDir, "latest.json"))
	if err := store.Load(); err != nil {
		log.Printf("warn: could not load existing snapshot: %v", err)
	}

	// //go:embed cannot reach outside its own package directory, so the SPA
	// build is embedded once in package web and shared by both binaries.
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		log.Fatalf("embedded assets: %v", err)
	}

	api := server.New(store).Mux()

	err = wails.Run(&options.App{
		Title:  "kscope",
		Width:  1400,
		Height: 900,
		AssetServer: &assetserver.Options{
			Assets: dist,
			// Middleware wraps the outermost handler in both dev and
			// production, so it is the only hook that intercepts /api/*
			// before dev mode forwards unmatched requests to Vite (and
			// before non-GET requests get a blanket 405).
			Middleware: apiMiddleware(api),
		},
		OnStartup: func(_ context.Context) {
			log.Printf("kscope desktop started (data-dir=%s)", *dataDir)
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// apiMiddleware routes API requests to the existing ServeMux and lets
// everything else fall through to Wails' normal asset handling.
func apiMiddleware(api http.Handler) assetserver.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if server.IsAPIPath(r.URL.Path) {
				api.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
