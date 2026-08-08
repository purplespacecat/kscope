package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/purplespacecat/kscope/internal/graph"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Frontend event names. The frontend subscribes to these through the injected
// window.runtime global rather than importing generated bindings, so the plain
// browser build keeps working.
const (
	eventRefresh  = "kscope:refresh"
	eventRecenter = "kscope:recenter"
	eventFocus    = "kscope:focus"
)

// App holds the Wails context and the bits of native behaviour that HTTP
// cannot express: file dialogs, window geometry, menu commands. Everything
// else still goes through the HTTP API.
type App struct {
	ctx       context.Context
	store     *graph.Store
	stateFile string
}

func newApp(store *graph.Store, dataDir string) *App {
	return &App{
		store:     store,
		stateFile: filepath.Join(dataDir, "window.json"),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.restoreWindow()
}

// beforeClose persists geometry and lets the close proceed.
//
// This must not be OnShutdown: by then GTK has destroyed the window and every
// geometry read returns 0. OnBeforeClose fires while the window is still
// alive.
func (a *App) beforeClose(_ context.Context) bool {
	a.saveWindow()
	return false // false = don't prevent the close
}

// windowState is the persisted geometry. Maximised is tracked separately
// because reading size while maximised would record the screen dimensions and
// lose the size to restore down to.
//
// Position is deliberately absent: under Wayland a client cannot query or set
// its own absolute window position, so persisting it would store zeros and
// restoring it would silently do nothing. Size and maximised state do work.
type windowState struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised"`
}

const (
	defaultWidth  = 1400
	defaultHeight = 900
)

func (a *App) restoreWindow() {
	data, err := os.ReadFile(a.stateFile)
	if err != nil {
		return // first run, or unreadable — the configured defaults stand
	}
	var st windowState
	if err := json.Unmarshal(data, &st); err != nil {
		log.Printf("warn: ignoring corrupt window state: %v", err)
		return
	}
	// Guard against junk: a zero or absurdly small size would open a window
	// the user can't interact with.
	if st.Width >= 640 && st.Height >= 480 {
		wruntime.WindowSetSize(a.ctx, st.Width, st.Height)
		log.Printf("restored window %dx%d (maximised=%t)", st.Width, st.Height, st.Maximised)
	}
	if st.Maximised {
		wruntime.WindowMaximise(a.ctx)
	}
}

func (a *App) saveWindow() {
	st := windowState{Maximised: wruntime.WindowIsMaximised(a.ctx)}
	if st.Maximised {
		// Keep the pre-maximise size so "restore down" returns somewhere sane.
		if prev := a.loadState(); prev != nil && prev.Width > 0 {
			st.Width, st.Height = prev.Width, prev.Height
		} else {
			st.Width, st.Height = defaultWidth, defaultHeight
		}
	} else {
		st.Width, st.Height = wruntime.WindowGetSize(a.ctx)
	}

	// Never persist a degenerate size over a good one.
	if st.Width < 640 || st.Height < 480 {
		log.Printf("warn: implausible window size %dx%d, not persisting", st.Width, st.Height)
		return
	}

	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.stateFile), 0o755); err != nil {
		log.Printf("warn: could not persist window state: %v", err)
		return
	}
	if err := os.WriteFile(a.stateFile, data, 0o644); err != nil {
		log.Printf("warn: could not persist window state: %v", err)
	}
}

func (a *App) loadState() *windowState {
	data, err := os.ReadFile(a.stateFile)
	if err != nil {
		return nil
	}
	var st windowState
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

// SaveManifest is a bound method: the frontend calls it as
// window.go.main.App.SaveManifest(...). It opens a native save dialog and
// writes the YAML the caller already has in hand — sending the text across
// rather than a node ID keeps this decoupled from the store and means the
// dialog saves exactly what the user is looking at.
//
// Returns the chosen path, or "" if the user cancelled.
func (a *App) SaveManifest(suggestedName, yaml string) (string, error) {
	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		DefaultFilename: sanitizeFilename(suggestedName) + ".yaml",
		Title:           "Save manifest",
		Filters: []wruntime.FileFilter{
			{DisplayName: "YAML (*.yaml, *.yml)", Pattern: "*.yaml;*.yml"},
			{DisplayName: "All files (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("save dialog: %w", err)
	}
	if path == "" {
		return "", nil // cancelled
	}
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// sanitizeFilename turns a node ID ("apps/deployment/ns/name") into something
// usable as a filename.
func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "manifest"
	}
	repl := strings.NewReplacer("/", "-", `\`, "-", ":", "-", string(os.PathSeparator), "-")
	return repl.Replace(s)
}
