// Package paths resolves the per-user locations kscope writes to.
//
// The CLI has always defaulted to "./data", which is fine when you launch it
// from the repo. A desktop app launched from a menu entry inherits an
// arbitrary working directory, so it needs an absolute, per-user location
// instead. Both binaries share this resolver; --data-dir still overrides it.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// appDir is the single subdirectory name used under whichever base the
// platform conventions pick.
const appDir = "kscope"

// DataDir returns the directory for persisted snapshots (latest.json and the
// manifests.json sidecar).
//
// On Linux this follows the XDG Base Directory spec: $XDG_DATA_HOME, falling
// back to ~/.local/share. Go's stdlib has os.UserConfigDir and os.UserCacheDir
// but no UserDataDir, so the Linux case is spelled out here. Snapshots are
// neither config (the user doesn't hand-edit them) nor cache (they are the
// source of truth for the UI and must survive a cache sweep), so the data
// directory is the correct home.
//
// On macOS and Windows there is no separate data location by convention, so
// os.UserConfigDir is right: ~/Library/Application Support and %AppData%.
//
// If the user's home directory cannot be determined — a rare, broken
// environment — it falls back to "./data" so the caller still gets something
// usable rather than an error to plumb through startup.
func DataDir() string {
	if base := linuxDataHome(); base != "" {
		return filepath.Join(base, appDir)
	}
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, appDir)
	}
	return "data"
}

// linuxDataHome returns the XDG data base on Linux and "" everywhere else
// (including when the home directory is unknown).
func linuxDataHome() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	// The spec says a relative $XDG_DATA_HOME must be ignored.
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share")
}
