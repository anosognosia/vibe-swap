package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"vibeswap/pkg/config"
)

func TestFileAdapter(t *testing.T) {
	// Create temporary directory for tests
	tmpDir, err := os.MkdirTemp("", "vibeswap-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set home dir or bypass GetProfilesDir by mocking config paths if needed,
	// but since we want to test FileAdapter's Save/Load:
	// Let's create a custom FileAdapter and override profile paths?
	// Actually, FileAdapter uses config.GetProfilesDir().
	// We can set os.Setenv("HOME", tmpDir) to sandbox all paths!
	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	// Create a mock source credential file
	srcPath := filepath.Join(tmpDir, "credentials.json")
	srcData := `{"token": "test-token-value"}`
	if err := os.WriteFile(srcPath, []byte(srcData), 0600); err != nil {
		t.Fatalf("failed to write mock credentials: %v", err)
	}

	target := config.Target{
		Name: "Mock Target",
		Type: config.TypeFile,
		Path: srcPath,
	}

	fa := &FileAdapter{}
	targetID := "mock_target"
	profileName := "test_profile"

	// 1. Check IsInstalled
	if !fa.IsInstalled(target) {
		t.Error("expected target to be installed")
	}

	// 2. Save profile
	if err := fa.Save(target, targetID, profileName); err != nil {
		t.Fatalf("failed to save profile: %v", err)
	}

	// Verify profile file exists and has same content
	profilePath, err := fa.getProfilePath(targetID, profileName)
	if err != nil {
		t.Fatalf("failed to get profile path: %v", err)
	}
	profData, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("failed to read profile file: %v", err)
	}
	if string(profData) != srcData {
		t.Errorf("expected profile content %q, got %q", srcData, string(profData))
	}

	// 3. Modify source file, then Load profile back to restore
	newSrcData := `{"token": "different-token"}`
	if err := os.WriteFile(srcPath, []byte(newSrcData), 0600); err != nil {
		t.Fatalf("failed to write modified credentials: %v", err)
	}

	if err := fa.Load(target, targetID, profileName); err != nil {
		t.Fatalf("failed to load profile: %v", err)
	}

	restoredData, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("failed to read restored credentials: %v", err)
	}
	if string(restoredData) != srcData {
		t.Errorf("expected restored content %q, got %q", srcData, string(restoredData))
	}
}

func TestJSONKeyAdapter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	// Create a mock source JSON config file
	srcPath := filepath.Join(tmpDir, "config.json")
	srcMap := map[string]interface{}{
		"theme": "dark",
		"auth": map[string]interface{}{
			"token": "secret-jwt-token",
		},
	}
	srcData, _ := json.Marshal(srcMap)
	if err := os.WriteFile(srcPath, srcData, 0600); err != nil {
		t.Fatalf("failed to write mock config: %v", err)
	}

	target := config.Target{
		Name: "Mock JSON Key Target",
		Type: config.TypeJSONKey,
		Path: srcPath,
		Key:  "auth.token",
	}

	ja := &JSONKeyAdapter{}
	targetID := "mock_json_target"
	profileName := "test_profile"

	// 1. Save profile
	if err := ja.Save(target, targetID, profileName); err != nil {
		t.Fatalf("failed to save profile: %v", err)
	}

	// Verify profile file exists and contains the correct extracted value
	profilePath, err := ja.getProfilePath(targetID, profileName)
	if err != nil {
		t.Fatalf("failed to get profile path: %v", err)
	}
	profData, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("failed to read profile file: %v", err)
	}

	var kv JSONKeyValue
	if err := json.Unmarshal(profData, &kv); err != nil {
		t.Fatalf("failed to parse profile value: %v", err)
	}
	if kv.Value != "secret-jwt-token" {
		t.Errorf("expected saved value %q, got %q", "secret-jwt-token", kv.Value)
	}

	// 2. Modify value in source JSON, then Load profile back to restore
	srcMap["auth"].(map[string]interface{})["token"] = "new-different-token"
	newSrcData, _ := json.Marshal(srcMap)
	_ = os.WriteFile(srcPath, newSrcData, 0600)

	if err := ja.Load(target, targetID, profileName); err != nil {
		t.Fatalf("failed to load profile: %v", err)
	}

	// Verify that ONLY the auth.token was updated, keeping "theme" as "dark"
	restoredData, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("failed to read restored config: %v", err)
	}

	var restoredMap map[string]interface{}
	_ = json.Unmarshal(restoredData, &restoredMap)

	themeVal := restoredMap["theme"].(string)
	if themeVal != "dark" {
		t.Errorf("expected theme to remain %q, got %q", "dark", themeVal)
	}

	tokenVal := restoredMap["auth"].(map[string]interface{})["token"].(string)
	if tokenVal != "secret-jwt-token" {
		t.Errorf("expected token to be restored to %q, got %q", "secret-jwt-token", tokenVal)
	}
}
