package engine

import (
	"os"
	"path/filepath"
	"testing"
	"vibeswap/pkg/config"
)

func TestDeleteProfile(t *testing.T) {
	// Create temporary directory for tests
	tmpDir, err := os.MkdirTemp("", "vibeswap-engine-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	// Set up config dir and profiles dir
	configDir := filepath.Join(tmpDir, ".config", "vibeswap")
	profilesDir := filepath.Join(configDir, "profiles")
	_ = os.MkdirAll(filepath.Join(profilesDir, "mock_target"), 0755)

	// 1. Create a dummy file profile
	fileProfile := filepath.Join(profilesDir, "mock_target", "profile1.json")
	_ = os.WriteFile(fileProfile, []byte(`{}`), 0600)

	// 2. Create a dummy directory profile
	dirProfile := filepath.Join(profilesDir, "mock_target", "profile2")
	_ = os.MkdirAll(dirProfile, 0755)
	_ = os.WriteFile(filepath.Join(dirProfile, "file.txt"), []byte(`data`), 0600)

	// Set active profile in state
	state := &config.ActiveState{
		Targets: map[string]string{
			"mock_target": "profile1",
		},
	}
	_ = config.SaveActiveState(state)

	// Test Delete File Profile
	err = DeleteProfile("mock_target", "profile1")
	if err != nil {
		t.Fatalf("unexpected error deleting file profile: %v", err)
	}

	if _, err := os.Stat(fileProfile); !os.IsNotExist(err) {
		t.Error("expected file profile to be deleted")
	}

	// Verify active state is cleaned up
	state, err = config.LoadActiveState()
	if err != nil {
		t.Fatalf("failed to load active state: %v", err)
	}
	if state.Targets["mock_target"] == "profile1" {
		t.Error("expected active state to be cleared")
	}

	// Test Delete Directory Profile
	err = DeleteProfile("mock_target", "profile2")
	if err != nil {
		t.Fatalf("unexpected error deleting dir profile: %v", err)
	}

	if _, err := os.Stat(dirProfile); !os.IsNotExist(err) {
		t.Error("expected dir profile to be deleted")
	}
}

func TestRenameProfile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-engine-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		t.Fatalf("failed to get profiles dir: %v", err)
	}
	targetDir := filepath.Join(profilesDir, "mock_target")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}

	fileProfile := filepath.Join(targetDir, "personal.json")
	if err := os.WriteFile(fileProfile, []byte(`{"token":"1"}`), 0600); err != nil {
		t.Fatalf("failed to create file profile: %v", err)
	}
	dirProfile := filepath.Join(targetDir, "work")
	if err := os.MkdirAll(dirProfile, 0755); err != nil {
		t.Fatalf("failed to create directory profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirProfile, "profile.json"), []byte(`{}`), 0600); err != nil {
		t.Fatalf("failed to create directory profile file: %v", err)
	}

	state := &config.ActiveState{Targets: map[string]string{"mock_target": "work"}}
	if err := config.SaveActiveState(state); err != nil {
		t.Fatalf("failed to save active state: %v", err)
	}

	if err := RenameProfile("mock_target", "personal", "home"); err != nil {
		t.Fatalf("failed to rename file profile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "home.json")); err != nil {
		t.Fatalf("expected renamed file profile: %v", err)
	}
	if _, err := os.Stat(fileProfile); !os.IsNotExist(err) {
		t.Fatalf("expected old file profile to be gone, got %v", err)
	}

	if err := RenameProfile("mock_target", "work", "wtd"); err != nil {
		t.Fatalf("failed to rename directory profile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "wtd", "profile.json")); err != nil {
		t.Fatalf("expected renamed directory profile: %v", err)
	}
	state, err = config.LoadActiveState()
	if err != nil {
		t.Fatalf("failed to load active state: %v", err)
	}
	if state.Targets["mock_target"] != "wtd" {
		t.Fatalf("expected active profile to be renamed to wtd, got %q", state.Targets["mock_target"])
	}

	if err := RenameProfile("mock_target", "wtd", "home"); err == nil {
		t.Fatal("expected rename collision to fail")
	}
}

func TestListProfiles(t *testing.T) {
	// Create temporary directory for tests
	tmpDir, err := os.MkdirTemp("", "vibeswap-engine-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	// Set up config dir and profiles dir
	configDir := filepath.Join(tmpDir, ".config", "vibeswap")
	profilesDir := filepath.Join(configDir, "profiles")
	_ = os.MkdirAll(filepath.Join(profilesDir, "mock_target"), 0755)

	// 1. Create a file profile
	fileProfile := filepath.Join(profilesDir, "mock_target", "file_profile.json")
	_ = os.WriteFile(fileProfile, []byte(`{}`), 0600)

	// 2. Create a directory profile
	dirProfile := filepath.Join(profilesDir, "mock_target", "dir_profile")
	_ = os.MkdirAll(dirProfile, 0755)

	profiles, err := ListProfiles()
	if err != nil {
		t.Fatalf("failed to list profiles: %v", err)
	}

	targetProfiles := profiles["mock_target"]
	if len(targetProfiles) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(targetProfiles))
	}

	hasFileProf := false
	hasDirProf := false
	for _, p := range targetProfiles {
		if p == "file_profile" {
			hasFileProf = true
		}
		if p == "dir_profile" {
			hasDirProf = true
		}
	}

	if !hasFileProf {
		t.Error("expected to find file_profile")
	}
	if !hasDirProf {
		t.Error("expected to find dir_profile")
	}
}

func TestListProfilesSkipsJSONProfilesForElectronTargets(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-engine-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	cfg := config.GetDefaultConfig()
	cfg.Targets = map[string]config.Target{
		"desktop_target": {
			Name: "Desktop Target",
			Type: config.TypeElectron,
			Path: filepath.Join(tmpDir, "DesktopApp"),
		},
	}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		t.Fatalf("failed to get profiles dir: %v", err)
	}
	targetDir := filepath.Join(profilesDir, "desktop_target")
	if err := os.MkdirAll(filepath.Join(targetDir, "dir_profile"), 0755); err != nil {
		t.Fatalf("failed to create directory profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "legacy.json"), []byte(`{}`), 0600); err != nil {
		t.Fatalf("failed to create legacy json profile: %v", err)
	}

	profiles, err := ListProfiles()
	if err != nil {
		t.Fatalf("failed to list profiles: %v", err)
	}

	targetProfiles := profiles["desktop_target"]
	if len(targetProfiles) != 1 || targetProfiles[0] != "dir_profile" {
		t.Fatalf("expected only directory profile, got %#v", targetProfiles)
	}
}

func TestDeleteActiveWrappedProfile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-engine-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	// Set up config dir, target config, and active state
	configDir := filepath.Join(tmpDir, ".config", "vibeswap")
	_ = os.MkdirAll(configDir, 0755)

	targetDir := filepath.Join(tmpDir, "mock_wrapped_dir")
	// Make it a symlink initially to profile dir
	profilesDir := filepath.Join(configDir, "profiles")
	profileDir := filepath.Join(profilesDir, "claude_cli", "personal")
	_ = os.MkdirAll(profileDir, 0755)
	_ = os.Symlink(profileDir, targetDir)

	cfg := &config.Config{
		Targets: map[string]config.Target{
			"claude_cli": {
				Name:   "Claude Code CLI",
				Type:   config.TypeWrappedDir,
				Path:   targetDir,
				EnvVar: "CLAUDE_CONFIG_DIR",
				Binary: "claude",
			},
		},
	}
	_ = config.SaveConfig(cfg)

	state := &config.ActiveState{
		Targets: map[string]string{
			"claude_cli": "personal",
		},
	}
	_ = config.SaveActiveState(state)

	// Verify targetDir is a symlink
	fi, err := os.Lstat(targetDir)
	if err != nil {
		t.Fatalf("failed to stat targetDir: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected targetDir to be a symlink initially")
	}

	// Delete active profile
	err = DeleteProfile("claude_cli", "personal")
	if err != nil {
		t.Fatalf("unexpected error deleting profile: %v", err)
	}

	// Verify targetDir is now a real physical directory and not a symlink
	fi, err = os.Lstat(targetDir)
	if err != nil {
		t.Fatalf("failed to stat targetDir after deletion: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("expected targetDir to not be a symlink after deletion")
	}
	if !fi.IsDir() {
		t.Error("expected targetDir to be a directory after deletion")
	}
}
