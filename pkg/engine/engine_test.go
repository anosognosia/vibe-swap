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
