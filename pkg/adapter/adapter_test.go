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

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	fa := &FileAdapter{}

	// --- Test Single File ---
	t.Run("Single File", func(t *testing.T) {
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

		targetID := "mock_target"
		profileName := "test_profile"

		if !fa.IsInstalled(target) {
			t.Error("expected target to be installed")
		}

		if err := fa.Save(target, targetID, profileName); err != nil {
			t.Fatalf("failed to save profile: %v", err)
		}

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
	})

	// --- Test Multiple Files ---
	t.Run("Multiple Files", func(t *testing.T) {
		path1 := filepath.Join(tmpDir, "file1.json")
		data1 := `{"id": "1"}`
		path2 := filepath.Join(tmpDir, "file2.json")
		data2 := `{"id": "2"}`

		_ = os.WriteFile(path1, []byte(data1), 0600)
		_ = os.WriteFile(path2, []byte(data2), 0600)

		target := config.Target{
			Name:  "Mock Multi Target",
			Type:  config.TypeFile,
			Paths: []string{path1, path2},
		}

		targetID := "mock_multi_target"
		profileName := "test_profile"

		if !fa.IsInstalled(target) {
			t.Error("expected target to be installed")
		}

		if err := fa.Save(target, targetID, profileName); err != nil {
			t.Fatalf("failed to save profile: %v", err)
		}

		// Change files
		_ = os.WriteFile(path1, []byte(`{"id": "different1"}`), 0600)
		_ = os.WriteFile(path2, []byte(`{"id": "different2"}`), 0600)

		// Restore profile
		if err := fa.Load(target, targetID, profileName); err != nil {
			t.Fatalf("failed to load profile: %v", err)
		}

		// Verify files are restored
		res1, _ := os.ReadFile(path1)
		res2, _ := os.ReadFile(path2)

		if string(res1) != data1 {
			t.Errorf("expected file1 to be %q, got %q", data1, string(res1))
		}
		if string(res2) != data2 {
			t.Errorf("expected file2 to be %q, got %q", data2, string(res2))
		}
	})

	// --- Test Directory ---
	t.Run("Directory", func(t *testing.T) {
		dirPath := filepath.Join(tmpDir, "mock_dir")
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			t.Fatalf("failed to create mock dir: %v", err)
		}
		file1 := filepath.Join(dirPath, "file1.txt")
		file2 := filepath.Join(dirPath, "sub", "file2.txt")
		_ = os.MkdirAll(filepath.Dir(file2), 0755)

		_ = os.WriteFile(file1, []byte("content1"), 0600)
		_ = os.WriteFile(file2, []byte("content2"), 0600)

		target := config.Target{
			Name: "Mock Dir Target",
			Type: config.TypeFile,
			Path: dirPath,
		}

		targetID := "mock_dir_target"
		profileName := "dir_profile"

		if !fa.IsInstalled(target) {
			t.Error("expected directory to be installed")
		}

		if err := fa.Save(target, targetID, profileName); err != nil {
			t.Fatalf("failed to save directory: %v", err)
		}

		// Delete files or modify them
		_ = os.WriteFile(file1, []byte("different1"), 0600)
		_ = os.Remove(file2)

		// Restore profile
		if err := fa.Load(target, targetID, profileName); err != nil {
			t.Fatalf("failed to load directory profile: %v", err)
		}

		// Verify files are restored
		res1, _ := os.ReadFile(file1)
		res2, _ := os.ReadFile(file2)

		if string(res1) != "content1" {
			t.Errorf("expected file1 to be restored, got %q", string(res1))
		}
		if string(res2) != "content2" {
			t.Errorf("expected file2 to be restored, got %q", string(res2))
		}
	})
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
