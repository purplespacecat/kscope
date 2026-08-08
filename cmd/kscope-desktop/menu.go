package main

import (
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// appMenu builds the application menu. The view commands don't act directly:
// the frontend owns that state (which scope is selected, where the canvas is
// panned), so the menu emits events and React does the work.
func (a *App) appMenu() *menu.Menu {
	m := menu.NewMenu()

	file := m.AddSubmenu("File")
	file.AddText("Run discovery", keys.CmdOrCtrl("r"), func(_ *menu.CallbackData) {
		wruntime.EventsEmit(a.ctx, eventRefresh)
	})
	file.AddSeparator()
	file.AddText("Quit", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		wruntime.Quit(a.ctx)
	})

	view := m.AddSubmenu("View")
	view.AddText("Re-center graph", keys.CmdOrCtrl("0"), func(_ *menu.CallbackData) {
		wruntime.EventsEmit(a.ctx, eventRecenter)
	})

	help := m.AddSubmenu("Help")
	help.AddText("About kscope", nil, func(_ *menu.CallbackData) {
		a.showAbout()
	})

	return m
}

func (a *App) showAbout() {
	body := "An interactive map of a Kubernetes cluster.\n\n" + a.snapshotSummary()
	_, err := wruntime.MessageDialog(a.ctx, wruntime.MessageDialogOptions{
		Type:    wruntime.InfoDialog,
		Title:   "kscope",
		Message: body,
	})
	if err != nil {
		// A failed dialog is not worth interrupting the session over.
		fmt.Printf("warn: about dialog: %v\n", err)
	}
}

// snapshotSummary describes what is currently loaded, which is the thing
// actually worth showing in an About box for this app.
func (a *App) snapshotSummary() string {
	snap, err := a.store.Get()
	if err != nil {
		return "No snapshot loaded yet."
	}
	return fmt.Sprintf("Context: %s\nSnapshot: %s\n%d nodes, %d edges",
		snap.Cluster.Context,
		snap.Timestamp.Local().Format("2006-01-02 15:04:05"),
		len(snap.Nodes), len(snap.Edges))
}
