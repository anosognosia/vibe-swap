package adapter

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	t.Run("Multiple Files Skips Missing Optional Paths", func(t *testing.T) {
		path1 := filepath.Join(tmpDir, "present.json")
		missingPath := filepath.Join(tmpDir, "missing.json")
		data1 := `{"id": "present"}`

		_ = os.WriteFile(path1, []byte(data1), 0600)

		target := config.Target{
			Name:  "Mock Partial Multi Target",
			Type:  config.TypeFile,
			Paths: []string{missingPath, path1},
		}

		targetID := "mock_partial_multi_target"
		profileName := "test_profile"

		if !fa.IsInstalled(target) {
			t.Error("expected target to be installed when at least one configured file exists")
		}

		if err := fa.Save(target, targetID, profileName); err != nil {
			t.Fatalf("failed to save partial multi-file profile: %v", err)
		}

		_ = os.WriteFile(path1, []byte(`{"id": "changed"}`), 0600)

		if err := fa.Load(target, targetID, profileName); err != nil {
			t.Fatalf("failed to load partial multi-file profile: %v", err)
		}

		res1, _ := os.ReadFile(path1)
		if string(res1) != data1 {
			t.Errorf("expected present file to be %q, got %q", data1, string(res1))
		}
		if _, err := os.Stat(missingPath); !os.IsNotExist(err) {
			t.Error("expected missing optional path to remain absent")
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

func TestWrappedDirAdapter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	wa := &WrappedDirAdapter{}

	srcDir := filepath.Join(tmpDir, "mock_wrapped_dir")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create source dir: %v", err)
	}
	file1 := filepath.Join(srcDir, "token.txt")
	_ = os.WriteFile(file1, []byte("token-v1"), 0600)

	target := config.Target{
		Name:   "Mock Wrapped Target",
		Type:   config.TypeWrappedDir,
		Path:   srcDir,
		EnvVar: "WRAPPED_CONFIG_DIR",
		Binary: "wrapped",
	}

	targetID := "mock_wrapped_target"
	profileName := "test_profile"

	if err := wa.Save(target, targetID, profileName); err != nil {
		t.Fatalf("failed to save wrapped dir profile: %v", err)
	}

	// Verify profile directory exists and has the correct files
	profilesDir, _ := config.GetProfilesDir()
	profilePath := filepath.Join(profilesDir, targetID, profileName)
	resFile := filepath.Join(profilePath, "token.txt")
	resData, err := os.ReadFile(resFile)
	if err != nil {
		t.Fatalf("failed to read saved token: %v", err)
	}
	if string(resData) != "token-v1" {
		t.Errorf("expected saved token to be %q, got %q", "token-v1", string(resData))
	}

	// Verify Load updates/checks successfully
	if err := wa.Load(target, targetID, profileName); err != nil {
		t.Fatalf("failed to load wrapped dir profile: %v", err)
	}

	// Verify that srcDir is now a symlink pointing to profilePath
	fi, err := os.Lstat(srcDir)
	if err != nil {
		t.Fatalf("failed to stat srcDir after load: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected srcDir to be a symlink after load, but it was not")
	}

	targetLink, err := os.Readlink(srcDir)
	if err != nil {
		t.Fatalf("failed to read symlink srcDir: %v", err)
	}
	if targetLink != profilePath {
		t.Errorf("expected symlink to point to %s, got %s", profilePath, targetLink)
	}

	// Verify we can read file through the symlink
	symlinkedFile := filepath.Join(srcDir, "token.txt")
	symlinkedData, err := os.ReadFile(symlinkedFile)
	if err != nil {
		t.Fatalf("failed to read token through symlink: %v", err)
	}
	if string(symlinkedData) != "token-v1" {
		t.Errorf("expected token content via symlink to be %q, got %q", "token-v1", string(symlinkedData))
	}
}

func TestSyncDirSkipsUnchangedAndRemovesStaleFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-sync-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	srcDir := filepath.Join(tmpDir, "src")
	dstDir := filepath.Join(tmpDir, "dst")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0755); err != nil {
		t.Fatalf("failed to create source dir: %v", err)
	}

	srcFile := filepath.Join(srcDir, "sub", "token.txt")
	if err := os.WriteFile(srcFile, []byte("token-v1"), 0600); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}
	srcTime := time.Unix(1234, 0)
	if err := os.Chtimes(srcFile, srcTime, srcTime); err != nil {
		t.Fatalf("failed to set source file time: %v", err)
	}

	if err := syncDir(srcDir, dstDir); err != nil {
		t.Fatalf("failed initial sync: %v", err)
	}

	dstFile := filepath.Join(dstDir, "sub", "token.txt")
	firstInfo, err := os.Stat(dstFile)
	if err != nil {
		t.Fatalf("failed to stat copied file: %v", err)
	}
	if !firstInfo.ModTime().Equal(srcTime) {
		t.Fatalf("expected copied file mtime %v, got %v", srcTime, firstInfo.ModTime())
	}

	staleFile := filepath.Join(dstDir, "stale.txt")
	if err := os.WriteFile(staleFile, []byte("stale"), 0600); err != nil {
		t.Fatalf("failed to write stale file: %v", err)
	}

	if err := syncDir(srcDir, dstDir); err != nil {
		t.Fatalf("failed second sync: %v", err)
	}

	secondInfo, err := os.Stat(dstFile)
	if err != nil {
		t.Fatalf("failed to stat copied file after second sync: %v", err)
	}
	if !secondInfo.ModTime().Equal(firstInfo.ModTime()) {
		t.Fatalf("expected unchanged file mtime to remain %v, got %v", firstInfo.ModTime(), secondInfo.ModTime())
	}
	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be removed, got err %v", err)
	}
}

func TestElectronRelPath(t *testing.T) {
	root := filepath.Join("Users", "test", "Library", "Application Support", "Codex")
	path := filepath.Join(root, "Default", "Cookies")

	rel, err := electronRelPath(root, path)
	if err != nil {
		t.Fatalf("failed to derive relative path: %v", err)
	}
	if rel != filepath.Join("Default", "Cookies") {
		t.Fatalf("expected relative path %q, got %q", filepath.Join("Default", "Cookies"), rel)
	}

	external := filepath.Join("Users", "test", "Library", "Application Support", "OpenAI", "Codex")
	rel, err = electronRelPath(root, external)
	if err != nil {
		t.Fatalf("failed to derive external path: %v", err)
	}
	if !strings.HasPrefix(rel, "_external") {
		t.Fatalf("expected external path under _external, got %q", rel)
	}

}

func TestElectronAdapterSaveAndLoadFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-electron-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	root := filepath.Join(tmpDir, "Library", "Application Support", "Codex")
	cookiePath := filepath.Join(root, "Cookies")
	prefsPath := filepath.Join(root, "Default", "Preferences")
	sessionPath := filepath.Join(root, "Session Storage")
	if err := os.MkdirAll(filepath.Dir(prefsPath), 0755); err != nil {
		t.Fatalf("failed to create app dirs: %v", err)
	}
	if err := os.WriteFile(cookiePath, []byte("cookie-v1"), 0600); err != nil {
		t.Fatalf("failed to write cookie file: %v", err)
	}
	if err := os.WriteFile(prefsPath, []byte("prefs-v1"), 0600); err != nil {
		t.Fatalf("failed to write prefs file: %v", err)
	}

	target := config.Target{
		Name:  "Codex Desktop",
		Type:  config.TypeElectron,
		Path:  root,
		Paths: []string{cookiePath, prefsPath},
	}
	adp := &ElectronAdapter{}
	if err := adp.Save(target, "codex_desktop_test", "personal"); err != nil {
		t.Fatalf("failed to save electron profile: %v", err)
	}

	if err := os.WriteFile(cookiePath, []byte("cookie-v2"), 0600); err != nil {
		t.Fatalf("failed to mutate cookie file: %v", err)
	}
	if err := os.WriteFile(prefsPath, []byte("prefs-v2"), 0600); err != nil {
		t.Fatalf("failed to mutate prefs file: %v", err)
	}
	if err := os.MkdirAll(sessionPath, 0755); err != nil {
		t.Fatalf("failed to create stale session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionPath, "stale.log"), []byte("stale"), 0600); err != nil {
		t.Fatalf("failed to write stale session file: %v", err)
	}
	target.Paths = append(target.Paths, sessionPath)

	if err := adp.Load(target, "codex_desktop_test", "personal"); err != nil {
		t.Fatalf("failed to load electron profile: %v", err)
	}

	cookieData, err := os.ReadFile(cookiePath)
	if err != nil {
		t.Fatalf("failed to read restored cookie file: %v", err)
	}
	if string(cookieData) != "cookie-v1" {
		t.Fatalf("expected restored cookie data %q, got %q", "cookie-v1", string(cookieData))
	}

	prefsData, err := os.ReadFile(prefsPath)
	if err != nil {
		t.Fatalf("failed to read restored prefs file: %v", err)
	}
	if string(prefsData) != "prefs-v1" {
		t.Fatalf("expected restored prefs data %q, got %q", "prefs-v1", string(prefsData))
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale session dir to be removed, got err=%v", err)
	}
}

func TestSQLiteAdapterSaveAndLoadClaudeCookieRows(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}

	tmpDir, err := os.MkdirTemp("", "vibeswap-sqlite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	liveDB := filepath.Join(tmpDir, "Library", "Application Support", "Claude", "Cookies")
	if err := os.MkdirAll(filepath.Dir(liveDB), 0755); err != nil {
		t.Fatalf("failed to create app dir: %v", err)
	}

	createSQL := `
CREATE TABLE cookies (
  creation_utc INTEGER NOT NULL DEFAULT 0,
  host_key TEXT NOT NULL,
  name TEXT NOT NULL,
  value TEXT NOT NULL DEFAULT '',
  encrypted_value BLOB NOT NULL DEFAULT X'',
  path TEXT NOT NULL DEFAULT '/',
  expires_utc INTEGER NOT NULL DEFAULT 0,
  is_secure INTEGER NOT NULL DEFAULT 1,
  is_httponly INTEGER NOT NULL DEFAULT 1,
  last_access_utc INTEGER NOT NULL DEFAULT 0,
  UNIQUE(host_key, name, path)
);
INSERT INTO cookies (host_key, name, encrypted_value) VALUES
  ('.claude.ai', 'sessionKey', X'776F726B'),
  ('.claude.ai', 'lastActiveOrg', X'6F7267'),
  ('.claude.ai', 'routingHint', X'726F757465'),
  ('.example.com', 'sessionKey', X'6B656570');
`
	if err := exec.Command("sqlite3", liveDB, createSQL).Run(); err != nil {
		t.Fatalf("failed to create live cookie DB: %v", err)
	}

	target := config.Target{
		Name: "Claude Desktop App",
		Type: config.TypeSQLite,
		Path: liveDB,
		Keys: []string{"sessionKey", "sessionKeyLC", "routingHint", "lastActiveOrg"},
		Paths: []string{
			filepath.Join(filepath.Dir(liveDB), "Local Storage"),
		},
	}
	localStorageFile := filepath.Join(filepath.Dir(liveDB), "Local Storage", "leveldb", "000001.log")
	if err := os.MkdirAll(filepath.Dir(localStorageFile), 0755); err != nil {
		t.Fatalf("failed to create local storage dir: %v", err)
	}
	if err := os.WriteFile(localStorageFile, []byte("work-local-storage"), 0600); err != nil {
		t.Fatalf("failed to write local storage file: %v", err)
	}
	adp := &SQLiteAdapter{}
	if err := adp.Save(target, "claude_desktop_test", "work"); err != nil {
		t.Fatalf("failed to save SQLite profile: %v", err)
	}

	mutateSQL := `
DELETE FROM cookies WHERE host_key LIKE '%.claude.ai';
INSERT INTO cookies (host_key, name, encrypted_value) VALUES
  ('.claude.ai', 'sessionKey', X'706572736F6E616C'),
  ('.claude.ai', 'lastActiveOrg', X'706572736F6E616C6F7267'),
  ('.claude.ai', '__Host-extra', X'6578747261');
`
	if err := exec.Command("sqlite3", liveDB, mutateSQL).Run(); err != nil {
		t.Fatalf("failed to mutate live cookie DB: %v", err)
	}
	staleLocalStorageFile := filepath.Join(filepath.Dir(liveDB), "Local Storage", "leveldb", "stale.log")
	if err := os.WriteFile(localStorageFile, []byte("personal-local-storage"), 0600); err != nil {
		t.Fatalf("failed to mutate local storage file: %v", err)
	}
	if err := os.WriteFile(staleLocalStorageFile, []byte("stale"), 0600); err != nil {
		t.Fatalf("failed to write stale local storage file: %v", err)
	}

	if err := adp.Load(target, "claude_desktop_test", "work"); err != nil {
		t.Fatalf("failed to load SQLite profile: %v", err)
	}

	out, err := exec.Command("sqlite3", liveDB, "SELECT name || ':' || hex(encrypted_value) FROM cookies WHERE host_key = '.claude.ai' ORDER BY name;").Output()
	if err != nil {
		t.Fatalf("failed to inspect restored cookies: %v", err)
	}
	got := strings.TrimSpace(string(out))
	want := strings.Join([]string{
		"lastActiveOrg:6F7267",
		"routingHint:726F757465",
		"sessionKey:776F726B",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected restored Claude cookies:\nwant:\n%s\ngot:\n%s", want, got)
	}

	out, err = exec.Command("sqlite3", liveDB, "SELECT hex(encrypted_value) FROM cookies WHERE host_key = '.example.com' AND name = 'sessionKey';").Output()
	if err != nil {
		t.Fatalf("failed to inspect preserved cookie: %v", err)
	}
	if strings.TrimSpace(string(out)) != "6B656570" {
		t.Fatalf("expected unrelated cookie to be preserved, got %q", strings.TrimSpace(string(out)))
	}

	data, err := os.ReadFile(localStorageFile)
	if err != nil {
		t.Fatalf("failed to read restored local storage file: %v", err)
	}
	if string(data) != "work-local-storage" {
		t.Fatalf("expected restored local storage, got %q", string(data))
	}
	if _, err := os.Stat(staleLocalStorageFile); !os.IsNotExist(err) {
		t.Fatalf("expected stale local storage file to be removed, got err=%v", err)
	}
}

func TestWrappedDirAdapterClaudeUsesLiveKeychainService(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vibeswap-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	wa := &WrappedDirAdapter{}
	target := config.Target{
		Name:    "Claude Code CLI",
		Type:    config.TypeWrappedDir,
		Path:    "~/.claude",
		EnvVar:  "CLAUDE_CONFIG_DIR",
		Binary:  "claude",
		Service: "Claude Code-credentials",
	}

	service := wa.keychainService(target, "claude_cli")
	if service != "Claude Code-credentials" {
		t.Fatalf("expected live Claude service, got %q", service)
	}
}

func TestWrappedDirAdapterClaudeUsesUserKeychainAccount(t *testing.T) {
	oldUser := os.Getenv("USER")
	defer os.Setenv("USER", oldUser)
	os.Setenv("USER", "testuser")

	wa := &WrappedDirAdapter{}
	target := config.Target{
		Name:    "Claude Code CLI",
		Type:    config.TypeWrappedDir,
		Path:    "~/.claude",
		EnvVar:  "CLAUDE_CONFIG_DIR",
		Binary:  "claude",
		Service: "Claude Code-credentials",
	}

	account := wa.keychainAccount(target, "claude_cli", "Claude Code-credentials")
	if account != "testuser" {
		t.Fatalf("expected Claude keychain account from USER, got %q", account)
	}
}
