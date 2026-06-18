package adapter

import (
	"encoding/json"
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newUserdataTarget builds a target whose symlink target is the given path.
// No app/process guards are set so the running-process check always passes
// in the test environment.
func newUserdataTarget(symlinkPath string) config.Target {
	return config.Target{
		Name:          "Mock Electron Userdata",
		Type:          config.TypeElectronUserdata,
		SymlinkTarget: symlinkPath,
	}
}

// withTempHome points the test process at a temporary HOME so config paths
// (profiles dir, config.json) do not pollute the developer's real state.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	prev := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", prev) })
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func TestElectronUserdataAdapter_SaveCreatesImmutableSnapshot(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("initial"), 0644)
	_ = os.WriteFile(filepath.Join(live, "Cookies"), []byte("cookies-v0"), 0644)

	target := newUserdataTarget(live)
	targetID := "mock"
	adp := &ElectronUserdataAdapter{}

	if err := adp.Save(target, targetID, "personal"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// After Save, the userData path should be a symlink pointing at live/.
	li, err := os.Lstat(live)
	if err != nil {
		t.Fatal(err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected live to be a symlink after Save")
	}
	linkTarget, _ := os.Readlink(live)
	if filepath.Base(linkTarget) != electronUserdataLiveName {
		t.Fatalf("symlink target should end in %q, got %q", electronUserdataLiveName, linkTarget)
	}

	// The snapshot should exist and contain the saved files.
	profilesDir, _ := config.GetProfilesDir()
	snapshotDir := filepath.Join(profilesDir, targetID, "personal")
	if _, err := os.Stat(filepath.Join(snapshotDir, "config.json")); err != nil {
		t.Fatalf("snapshot missing config.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snapshotDir, "Cookies")); err != nil {
		t.Fatalf("snapshot missing Cookies: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snapshotDir, electronUserdataProfileFile)); err != nil {
		t.Fatalf("snapshot missing metadata file: %v", err)
	}

	// Metadata should be valid.
	mb, _ := os.ReadFile(filepath.Join(snapshotDir, electronUserdataProfileFile))
	var meta electronUserdataProfile
	if err := json.Unmarshal(mb, &meta); err != nil {
		t.Fatalf("invalid metadata: %v", err)
	}
	if meta.Kind != electronUserdataProfileKind {
		t.Fatalf("metadata kind: %q", meta.Kind)
	}

	// live/ should still hold the original content (it was not touched).
	got, _ := os.ReadFile(filepath.Join(live, "config.json"))
	if string(got) != "initial" {
		t.Fatalf("live/ was modified by Save: %q (want 'initial')", got)
	}

	// .current should now point at "personal".
	current, _ := adp.currentSnapshotName(targetID)
	if current != "personal" {
		t.Fatalf(".current = %q (want 'personal')", current)
	}
}

func TestElectronUserdataAdapter_SnapshotIsImmutableToClaudeWrites(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v1"), 0644)

	target := newUserdataTarget(live)
	targetID := "mock"
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, targetID, "personal"); err != nil {
		t.Fatal(err)
	}

	// Simulate Claude writing through the live symlink (e.g. user signed in
	// to a different account and Claude updated config.json).
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v2-changed-by-claude"), 0644)

	// The snapshot must NOT have been modified.
	profilesDir, _ := config.GetProfilesDir()
	snap, _ := os.ReadFile(filepath.Join(profilesDir, targetID, "personal", "config.json"))
	if string(snap) != "v1" {
		t.Fatalf("snapshot was mutated by Claude's writes: %q (want 'v1')", snap)
	}
}

func TestElectronUserdataAdapter_SaveSnapshotsOnlySessionState(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(filepath.Join(live, "Local Storage", "leveldb"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "claude-code-sessions", "project", "session"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "local-agent-mode-sessions", "project"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "vm_bundles"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "Cache"), 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("token"), 0644)
	_ = os.WriteFile(filepath.Join(live, "Cookies"), []byte("cookies"), 0644)
	_ = os.WriteFile(filepath.Join(live, "Local Storage", "leveldb", "000001.log"), []byte("local"), 0644)
	_ = os.WriteFile(filepath.Join(live, "claude-code-sessions", "project", "session", "local_123.json"), []byte("desktop session"), 0644)
	_ = os.WriteFile(filepath.Join(live, "local-agent-mode-sessions", "project", "env.json"), []byte("agent session"), 0644)
	_ = os.WriteFile(filepath.Join(live, "claude_desktop_config.json"), []byte("mcp"), 0644)
	_ = os.WriteFile(filepath.Join(live, "vm_bundles", "vm.img"), []byte("heavy"), 0644)
	_ = os.WriteFile(filepath.Join(live, "Cache", "runtime.cache"), []byte("cache"), 0644)

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, "mock", "personal"); err != nil {
		t.Fatal(err)
	}

	snapshot := filepath.Join(profilesDir(t), "mock", "personal")
	for _, rel := range []string{
		"config.json",
		"Cookies",
		filepath.Join("Local Storage", "leveldb", "000001.log"),
	} {
		if _, err := os.Stat(filepath.Join(snapshot, rel)); err != nil {
			t.Fatalf("expected session item %s in snapshot: %v", rel, err)
		}
	}
	for _, rel := range []string{
		"claude_desktop_config.json",
		filepath.Join("vm_bundles", "vm.img"),
		filepath.Join("Cache", "runtime.cache"),
		filepath.Join("claude-code-sessions", "project", "session", "local_123.json"),
		filepath.Join("local-agent-mode-sessions", "project", "env.json"),
	} {
		if _, err := os.Stat(filepath.Join(snapshot, rel)); !os.IsNotExist(err) {
			t.Fatalf("expected shared/heavy item %s to stay out of snapshot, got %v", rel, err)
		}
	}
}

func TestElectronUserdataAdapter_LoadOldSnapshotPreservesLiveClaudeCodeSessions(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(filepath.Join(live, "claude-code-sessions", "project", "session"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "local-agent-mode-sessions", "project"), 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("live"), 0644)
	_ = os.WriteFile(filepath.Join(live, "claude-code-sessions", "project", "session", "local_123.json"), []byte("desktop session"), 0644)
	_ = os.WriteFile(filepath.Join(live, "local-agent-mode-sessions", "project", "env.json"), []byte("agent session"), 0644)

	target := newUserdataTarget(live)
	targetID := "mock"
	adp := &ElectronUserdataAdapter{}
	if err := adp.ensureLiveSymlink(target, targetID); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(profilesDir(t), targetID, "personal")
	if err := os.MkdirAll(snapshot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "config.json"), []byte("snapshot"), 0644); err != nil {
		t.Fatal(err)
	}
	writeElectronUserdataProfileMarker(t, snapshot, target.SymlinkTarget)

	if err := adp.Load(target, targetID, "personal"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(live, "config.json")); string(got) != "snapshot" {
		t.Fatalf("expected auth/session config from snapshot, got %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(live, "claude-code-sessions", "project", "session", "local_123.json")); err != nil || string(got) != "desktop session" {
		t.Fatalf("expected live Claude Code Desktop session to survive old snapshot load, data=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(live, "local-agent-mode-sessions", "project", "env.json")); err != nil || string(got) != "agent session" {
		t.Fatalf("expected live local agent session to survive old snapshot load, data=%q err=%v", got, err)
	}
}

func TestElectronUserdataAdapter_LoadNewSnapshotPreservesLiveClaudeCodeSessions(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(filepath.Join(live, "claude-code-sessions", "project", "session"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "local-agent-mode-sessions", "project"), 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("personal"), 0644)
	_ = os.WriteFile(filepath.Join(live, "claude-code-sessions", "project", "session", "local_123.json"), []byte("desktop session"), 0644)
	_ = os.WriteFile(filepath.Join(live, "local-agent-mode-sessions", "project", "env.json"), []byte("agent session"), 0644)

	target := newUserdataTarget(live)
	targetID := "mock"
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, targetID, "personal"); err != nil {
		t.Fatal(err)
	}

	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("work"), 0644)
	if err := adp.Save(target, targetID, "work"); err != nil {
		t.Fatal(err)
	}

	if err := adp.Load(target, targetID, "personal"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(live, "config.json")); string(got) != "personal" {
		t.Fatalf("expected auth/session config from snapshot, got %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(live, "claude-code-sessions", "project", "session", "local_123.json")); err != nil || string(got) != "desktop session" {
		t.Fatalf("expected live Claude Code Desktop session to survive new snapshot load, data=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(live, "local-agent-mode-sessions", "project", "env.json")); err != nil || string(got) != "agent session" {
		t.Fatalf("expected live local agent session to survive new snapshot load, data=%q err=%v", got, err)
	}
}

func TestElectronUserdataAdapter_LoadReplacesLiveFromSnapshot(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v1"), 0644)

	target := newUserdataTarget(live)
	targetID := "mock"
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, targetID, "personal"); err != nil {
		t.Fatal(err)
	}
	// Save a second snapshot with different live contents.
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v2"), 0644)
	if err := adp.Save(target, targetID, "work"); err != nil {
		t.Fatal(err)
	}

	// Now Load "personal": live/ should be replaced with personal's contents.
	if err := adp.Load(target, targetID, "personal"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(live, "config.json"))
	if string(got) != "v1" {
		t.Fatalf("after Load personal: %q (want 'v1')", got)
	}
	current, _ := adp.currentSnapshotName(targetID)
	if current != "personal" {
		t.Fatalf(".current = %q after Load", current)
	}

	// And Load "work": live/ should be replaced with work's contents.
	if err := adp.Load(target, targetID, "work"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(live, "config.json"))
	if string(got) != "v2" {
		t.Fatalf("after Load work: %q (want 'v2')", got)
	}

	// Both snapshots must be intact and untouched.
	profilesDir, _ := config.GetProfilesDir()
	p, _ := os.ReadFile(filepath.Join(profilesDir, targetID, "personal", "config.json"))
	if string(p) != "v1" {
		t.Fatalf("personal mutated: %q", p)
	}
	w, _ := os.ReadFile(filepath.Join(profilesDir, targetID, "work", "config.json"))
	if string(w) != "v2" {
		t.Fatalf("work mutated: %q", w)
	}
}

func TestElectronUserdataAdapter_LoadPreservesSharedDataAndClearsStaleSessionState(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(filepath.Join(live, "Local Storage", "leveldb"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "Session Storage"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "vm_bundles"), 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("personal"), 0644)
	_ = os.WriteFile(filepath.Join(live, "Local Storage", "leveldb", "personal.log"), []byte("personal-local"), 0644)
	_ = os.WriteFile(filepath.Join(live, "Session Storage", "personal.log"), []byte("stale-session"), 0644)
	_ = os.WriteFile(filepath.Join(live, "claude_desktop_config.json"), []byte("mcp"), 0644)
	_ = os.WriteFile(filepath.Join(live, "vm_bundles", "vm.img"), []byte("heavy"), 0644)

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, "mock", "personal"); err != nil {
		t.Fatal(err)
	}

	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("work"), 0644)
	_ = os.RemoveAll(filepath.Join(live, "Local Storage"))
	_ = os.MkdirAll(filepath.Join(live, "Local Storage", "leveldb"), 0755)
	_ = os.WriteFile(filepath.Join(live, "Local Storage", "leveldb", "work.log"), []byte("work-local"), 0644)
	_ = os.WriteFile(filepath.Join(live, "Session Storage", "work.log"), []byte("work-stale"), 0644)

	if err := adp.Load(target, "mock", "personal"); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(filepath.Join(live, "config.json")); string(got) != "personal" {
		t.Fatalf("expected restored session config, got %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(live, "Local Storage", "leveldb", "personal.log")); string(got) != "personal-local" {
		t.Fatalf("expected restored local storage, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(live, "Local Storage", "leveldb", "work.log")); !os.IsNotExist(err) {
		t.Fatalf("expected stale local storage to be removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(live, "Session Storage", "work.log")); !os.IsNotExist(err) {
		t.Fatalf("expected stale session storage to be removed, got %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(live, "claude_desktop_config.json")); string(got) != "mcp" {
		t.Fatalf("expected shared desktop config to be preserved, got %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(live, "vm_bundles", "vm.img")); string(got) != "heavy" {
		t.Fatalf("expected shared vm data to be preserved, got %q", got)
	}
}

func TestElectronUserdataAdapter_SaveRefusesDuplicateName(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v1"), 0644)

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, "mock", "personal"); err != nil {
		t.Fatal(err)
	}
	if err := adp.Save(target, "mock", "personal"); err == nil {
		t.Fatal("expected Save to refuse overwriting an existing snapshot")
	}
}

func TestElectronUserdataAdapter_SaveRefusesReservedLiveName(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, "mock", electronUserdataLiveName); err == nil {
		t.Fatal("expected Save to refuse profile name 'live'")
	}
}

// TestElectronUserdataAdapter_DeleteThenSaveOverwrites covers the CLI's
// "delete existing then save" overwrite flow. After deleting an existing
// snapshot, the same name can be saved again.
func TestElectronUserdataAdapter_DeleteThenSaveOverwrites(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v1"), 0644)

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, "mock", "p"); err != nil {
		t.Fatal(err)
	}
	// Modify live.
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v2"), 0644)
	// Try to save again with the same name → must refuse.
	if err := adp.Save(target, "mock", "p"); err == nil {
		t.Fatal("expected Save to refuse second save with same name")
	}
	// Delete and re-save — should succeed and capture v2.
	if err := os.RemoveAll(filepath.Join(profilesDir(t), "mock", "p")); err != nil {
		t.Fatal(err)
	}
	if err := adp.Save(target, "mock", "p"); err != nil {
		t.Fatalf("Save after delete: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(profilesDir(t), "mock", "p", "config.json"))
	if string(got) != "v2" {
		t.Fatalf("overwritten snapshot has %q (want 'v2')", got)
	}
}

func TestElectronUserdataAdapter_LoadRejectsInvalidSnapshot(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v1"), 0644)

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}

	// Manually create a "snapshot" that does NOT have the metadata file.
	profilesDir, _ := config.GetProfilesDir()
	bogus := filepath.Join(profilesDir, "mock", "bogus")
	_ = os.MkdirAll(bogus, 0700)
	_ = os.WriteFile(filepath.Join(bogus, "x"), []byte("x"), 0644)

	if err := adp.Load(target, "mock", "bogus"); err == nil {
		t.Fatal("expected Load to reject a snapshot without the metadata marker")
	}
}

func TestElectronUserdataAdapter_IsActiveProfile(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v1"), 0644)

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, "mock", "p1"); err != nil {
		t.Fatal(err)
	}
	active, err := adp.IsActiveProfile(target, "mock", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("expected p1 to be active after Save")
	}
	if err := adp.Load(target, "mock", "p1"); err != nil {
		t.Fatal(err)
	}
	active, _ = adp.IsActiveProfile(target, "mock", "p1")
	if !active {
		t.Fatal("expected p1 to still be active after Load")
	}
}

func TestElectronUserdataAdapter_IsInstalled(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	target := newUserdataTarget(filepath.Join(tmp, "Does-Not-Exist"))
	adp := &ElectronUserdataAdapter{}
	if adp.IsInstalled(target) {
		t.Fatal("expected IsInstalled=false for missing path")
	}
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)
	target = newUserdataTarget(live)
	if !adp.IsInstalled(target) {
		t.Fatal("expected IsInstalled=true when path is a real userData dir")
	}
	// Now do a Save which creates the symlink → live/.
	_ = os.WriteFile(filepath.Join(live, "x"), []byte("v1"), 0644)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v1"), 0644)
	if err := adp.Save(target, "mock", "p1"); err != nil {
		t.Fatal(err)
	}
	if !adp.IsInstalled(target) {
		t.Fatal("expected IsInstalled=true after Save creates the symlink")
	}
}

func TestElectronUserdataAdapter_ClearSessionPreservesSharedData(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(filepath.Join(live, "Local Storage", "leveldb"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "claude-code-sessions", "project", "session"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "local-agent-mode-sessions", "project"), 0755)
	_ = os.MkdirAll(filepath.Join(live, "vm_bundles"), 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("token"), 0644)
	_ = os.WriteFile(filepath.Join(live, "Cookies"), []byte("cookies"), 0644)
	_ = os.WriteFile(filepath.Join(live, "Local Storage", "leveldb", "000001.log"), []byte("local"), 0644)
	_ = os.WriteFile(filepath.Join(live, "claude-code-sessions", "project", "session", "local_123.json"), []byte("desktop session"), 0644)
	_ = os.WriteFile(filepath.Join(live, "local-agent-mode-sessions", "project", "env.json"), []byte("agent session"), 0644)
	_ = os.WriteFile(filepath.Join(live, "claude_desktop_config.json"), []byte("mcp"), 0644)
	_ = os.WriteFile(filepath.Join(live, "vm_bundles", "vm.img"), []byte("heavy"), 0644)

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}
	if err := adp.ClearSession(target, "mock"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(live, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("expected config.json to be cleared, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(live, "Cookies")); !os.IsNotExist(err) {
		t.Fatalf("expected Cookies to be cleared, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(live, "Local Storage")); !os.IsNotExist(err) {
		t.Fatalf("expected Local Storage to be cleared, got %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(live, "claude-code-sessions", "project", "session", "local_123.json")); err != nil || string(data) != "desktop session" {
		t.Fatalf("expected Claude Code Desktop sessions to remain, data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(live, "local-agent-mode-sessions", "project", "env.json")); err != nil || string(data) != "agent session" {
		t.Fatalf("expected local agent sessions to remain, data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(live, "claude_desktop_config.json")); err != nil || string(data) != "mcp" {
		t.Fatalf("expected shared desktop config to remain, data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(live, "vm_bundles", "vm.img")); err != nil || string(data) != "heavy" {
		t.Fatalf("expected shared vm data to remain, data=%q err=%v", data, err)
	}
	current, _ := adp.currentSnapshotName("mock")
	if current != "" {
		t.Fatalf("expected current snapshot to be cleared, got %q", current)
	}
}

func TestElectronUserdataAdapter_DuplicateSessionKeyWarning(t *testing.T) {
	if _, err := exec_lookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)

	// Build a fake Cookies DB with a sessionKey cookie.
	if err := writeFakeCookiesDB(filepath.Join(live, "Cookies"), "deadbeef"); err != nil {
		t.Fatal(err)
	}

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, "mock", "personal"); err != nil {
		t.Fatal(err)
	}

	// Save again with the same sessionKey — should warn.
	if err := writeFakeCookiesDB(filepath.Join(live, "Cookies"), "deadbeef"); err != nil {
		t.Fatal(err)
	}
	if err := adp.Save(target, "mock", "duplicate"); err == nil {
		t.Fatal("expected Save to refuse (or warn) when sessionKey matches an existing snapshot")
	} else if msg := err.Error(); !strings.Contains(msg, "vibeswap new-login claude_desktop_oauth") || strings.Contains(msg, "rm ~/Library/Application") || strings.Contains(msg, "sign out of Claude") {
		t.Fatalf("duplicate-session guidance should point at new-login without logout/cookie-rm advice, got: %s", msg)
	}
}

// TestElectronUserdataAdapter_CoWIsolation verifies the CoW clone really
// diverges when the live dir is modified through the symlink.
func TestElectronUserdataAdapter_CoWIsolation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only CoW behavior")
	}
	withTempHome(t)
	tmp := t.TempDir()
	live := filepath.Join(tmp, "Live")
	_ = os.MkdirAll(live, 0755)
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v1"), 0644)

	target := newUserdataTarget(live)
	adp := &ElectronUserdataAdapter{}
	if err := adp.Save(target, "mock", "p1"); err != nil {
		t.Fatal(err)
	}

	// live/ is now a symlink → liveDir inside profiles. Modify config.json
	// through the symlink and verify p1's big.bin is untouched.
	_ = os.WriteFile(filepath.Join(live, "config.json"), []byte("v2"), 0644)
	profilesDir, _ := config.GetProfilesDir()
	p1Config, _ := os.ReadFile(filepath.Join(profilesDir, "mock", "p1", "config.json"))
	if string(p1Config) != "v1" {
		t.Fatalf("p1's config.json was mutated: %q", p1Config)
	}
}

// exec_lookPath is a tiny wrapper so this file can avoid importing os/exec
// at the top (the helpers below only need it for one path).
func exec_lookPath(name string) (string, error) {
	// Use os/exec.LookPath under the hood.
	return execLookPath(name)
}

func execLookPath(name string) (string, error) {
	// We can't import os/exec only here cleanly; do it via the existing
	// exec.Command-style helper via os.Getenv PATH walk.
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

func profilesDir(t *testing.T) string {
	t.Helper()
	d, err := config.GetProfilesDir()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func writeElectronUserdataProfileMarker(t *testing.T, snapshotDir, symlinkPath string) {
	t.Helper()
	meta := electronUserdataProfile{
		Kind:        electronUserdataProfileKind,
		Source:      symlinkPath,
		SymlinkPath: symlinkPath,
		VibeSwap:    "vibeswap electron_userdata",
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, electronUserdataProfileFile), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// writeFakeCookiesDB creates a Chromium-shaped Cookies SQLite database with
// one sessionKey cookie whose encrypted_value is the given hex string. Used
// by the duplicate-detection test.
func writeFakeCookiesDB(path, encryptedHex string) error {
	_, err := sqliteOutput(path, fmt.Sprintf(
		"DROP TABLE IF EXISTS cookies;"+
			"CREATE TABLE cookies(host_key TEXT, name TEXT, encrypted_value BLOB);"+
			"INSERT INTO cookies VALUES('.claude.ai','sessionKey',X'%s');",
		encryptedHex,
	))
	return err
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd-length hex")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		var hi, lo byte
		var err error
		if hi, err = hexNibble(s[2*i]); err != nil {
			return nil, err
		}
		if lo, err = hexNibble(s[2*i+1]); err != nil {
			return nil, err
		}
		out[i] = (hi << 4) | lo
	}
	return out, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("invalid hex char %q", c)
}
