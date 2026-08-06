package paths

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDataDirIsAbsolute(t *testing.T) {
	// The whole point of this package: a desktop app launched from a menu
	// entry has an arbitrary CWD, so a relative path would resolve somewhere
	// unpredictable.
	if got := DataDir(); !filepath.IsAbs(got) {
		t.Fatalf("DataDir() = %q, want an absolute path", got)
	}
}

func TestDataDirHonoursXDGDataHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_DATA_HOME only applies on Linux")
	}
	t.Setenv("XDG_DATA_HOME", "/custom/data")

	want := filepath.Join("/custom/data", appDir)
	if got := DataDir(); got != want {
		t.Fatalf("DataDir() = %q, want %q", got, want)
	}
}

func TestDataDirIgnoresRelativeXDGDataHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_DATA_HOME only applies on Linux")
	}
	// The XDG spec says a relative base must be ignored, not joined.
	t.Setenv("XDG_DATA_HOME", "relative/path")
	t.Setenv("HOME", "/home/tester")

	want := filepath.Join("/home/tester", ".local", "share", appDir)
	if got := DataDir(); got != want {
		t.Fatalf("DataDir() = %q, want %q", got, want)
	}
}
