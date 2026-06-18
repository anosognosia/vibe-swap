package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmailFromProfileDirScansClaudeBrowserStorage(t *testing.T) {
	dir := t.TempDir()
	storageDir := filepath.Join(dir, "files", "IndexedDB", "https_claude.ai_0.indexeddb.blob", "1", "00")
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storageDir, "2"), []byte("currentUserEmail\x00person@gmail.com"), 0600); err != nil {
		t.Fatal(err)
	}

	if got := emailFromProfileDir(dir); got != "person@gmail.com" {
		t.Fatalf("expected browser storage email, got %q", got)
	}
}

func TestEmailFromProfileDirSkipsDependencyNoise(t *testing.T) {
	dir := t.TempDir()
	noiseDir := filepath.Join(dir, "live", "Claude Extensions", "example", "node_modules", "pkg")
	if err := os.MkdirAll(noiseDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noiseDir, "package.json"), []byte(`{"author":"noise@gmail.com"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if got := emailFromProfileDir(dir); got != "" {
		t.Fatalf("expected dependency email noise to be skipped, got %q", got)
	}
}
