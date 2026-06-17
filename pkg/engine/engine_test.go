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

func TestSwitchClaudeDesktopOAuthSwitchesClaudeCLICompanion(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-engine-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	desktopPath := filepath.Join(tmpDir, "Library", "Application Support", "Claude")
	if err := os.MkdirAll(desktopPath, 0755); err != nil {
		t.Fatalf("failed to create desktop dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(desktopPath, "config.json"), []byte("desktop-personal"), 0600); err != nil {
		t.Fatalf("failed to seed desktop config: %v", err)
	}

	cliLive := filepath.Join(tmpDir, ".claude-auth")
	if err := os.WriteFile(cliLive, []byte("cli-personal"), 0600); err != nil {
		t.Fatalf("failed to seed cli live file: %v", err)
	}

	cfg := &config.Config{Targets: map[string]config.Target{
		"claude_desktop_oauth": {
			Name:          "Claude Desktop OAuth",
			Type:          config.TypeElectronUserdata,
			SymlinkTarget: desktopPath,
		},
		"claude_cli": {
			Name: "Claude CLI",
			Type: config.TypeFile,
			Path: cliLive,
		},
	}}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		t.Fatalf("failed to get profiles dir: %v", err)
	}
	desktopProfile := filepath.Join(profilesDir, "claude_desktop_oauth", "work")
	if err := os.MkdirAll(desktopProfile, 0700); err != nil {
		t.Fatalf("failed to create desktop profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(desktopProfile, ".vibeswap-profile.json"), []byte(`{"kind":"electron_userdata"}`), 0600); err != nil {
		t.Fatalf("failed to create desktop profile marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(desktopProfile, "config.json"), []byte("desktop-work"), 0600); err != nil {
		t.Fatalf("failed to create desktop profile config: %v", err)
	}

	cliTargetDir := filepath.Join(profilesDir, "claude_cli")
	if err := os.MkdirAll(cliTargetDir, 0700); err != nil {
		t.Fatalf("failed to create cli profile dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cliTargetDir, "work.json"), []byte("cli-work"), 0600); err != nil {
		t.Fatalf("failed to create cli profile: %v", err)
	}

	if err := SwitchProfile("claude_desktop_oauth", "work"); err != nil {
		t.Fatalf("failed to switch desktop oauth profile: %v", err)
	}

	desktopData, err := os.ReadFile(filepath.Join(desktopPath, "config.json"))
	if err != nil {
		t.Fatalf("failed to read desktop config after switch: %v", err)
	}
	if string(desktopData) != "desktop-work" {
		t.Fatalf("expected desktop profile to switch, got %q", desktopData)
	}

	cliData, err := os.ReadFile(cliLive)
	if err != nil {
		t.Fatalf("failed to read cli live file after switch: %v", err)
	}
	if string(cliData) != "cli-work" {
		t.Fatalf("expected companion cli profile to switch, got %q", cliData)
	}

	state, err := config.LoadActiveState()
	if err != nil {
		t.Fatalf("failed to load active state: %v", err)
	}
	if state.Targets["claude_desktop_oauth"] != "work" || state.Targets["claude_cli"] != "work" {
		t.Fatalf("expected active state for desktop and cli to be work, got %#v", state.Targets)
	}
}

func TestClearTargetSessionClearsDesktopOAuthActiveStateOnly(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-engine-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	desktopPath := filepath.Join(tmpDir, "Library", "Application Support", "Claude")
	if err := os.MkdirAll(filepath.Join(desktopPath, "Local Storage"), 0755); err != nil {
		t.Fatalf("failed to create desktop dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(desktopPath, "config.json"), []byte("token"), 0600); err != nil {
		t.Fatalf("failed to seed desktop config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(desktopPath, "Local Storage", "state.log"), []byte("local"), 0600); err != nil {
		t.Fatalf("failed to seed local storage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(desktopPath, "claude_desktop_config.json"), []byte("mcp"), 0600); err != nil {
		t.Fatalf("failed to seed shared config: %v", err)
	}

	cfg := &config.Config{Targets: map[string]config.Target{
		"claude_desktop_oauth": {
			Name:          "Claude Desktop OAuth",
			Type:          config.TypeElectronUserdata,
			SymlinkTarget: desktopPath,
		},
		"claude_cli": {
			Name: "Claude CLI",
			Type: config.TypeFile,
			Path: filepath.Join(tmpDir, ".claude-auth"),
		},
	}}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}
	if err := config.SaveActiveState(&config.ActiveState{Targets: map[string]string{
		"claude_desktop_oauth": "work",
		"claude_cli":           "work",
	}}); err != nil {
		t.Fatalf("failed to save active state: %v", err)
	}

	if err := ClearTargetSession("claude_desktop_oauth"); err != nil {
		t.Fatalf("failed to clear session: %v", err)
	}

	if _, err := os.Stat(filepath.Join(desktopPath, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("expected config.json to be cleared, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(desktopPath, "Local Storage")); !os.IsNotExist(err) {
		t.Fatalf("expected Local Storage to be cleared, got %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(desktopPath, "claude_desktop_config.json")); err != nil || string(got) != "mcp" {
		t.Fatalf("expected shared desktop config to remain, got %q err=%v", got, err)
	}

	state, err := config.LoadActiveState()
	if err != nil {
		t.Fatalf("failed to load active state: %v", err)
	}
	if _, ok := state.Targets["claude_desktop_oauth"]; ok {
		t.Fatalf("expected desktop oauth active state to be cleared, got %#v", state.Targets)
	}
	if state.Targets["claude_cli"] != "work" {
		t.Fatalf("expected unrelated companion active state to remain, got %#v", state.Targets)
	}
}
