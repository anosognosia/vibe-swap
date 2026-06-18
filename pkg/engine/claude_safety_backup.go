package engine

import (
	"encoding/json"
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/adapter"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type claudeSafetyBackupManifest struct {
	Kind      string                     `json:"kind"`
	Reason    string                     `json:"reason"`
	CreatedAt string                     `json:"created_at"`
	Sources   []claudeSafetyBackupSource `json:"sources"`
}

type claudeSafetyBackupSource struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Target string `json:"target"`
	Exists bool   `json:"exists"`
	Error  string `json:"error,omitempty"`
}

var claudeDesktopCriticalState = []string{
	"config.json",
	"Preferences",
	"Cookies",
	"Cookies-journal",
	"Local Storage",
	"Session Storage",
	"IndexedDB",
	"WebStorage",
	"Network",
	"claude-code-sessions",
	"local-agent-mode-sessions",
	"git-worktrees.json",
	"window-state.json",
}

// CreateClaudeSafetyBackup snapshots Claude's coupled local state before any
// operation that can move account/session pointers. It intentionally includes
// both Claude Code CLI transcripts and Claude Desktop's session metadata.
func CreateClaudeSafetyBackup(reason string) (string, error) {
	configDir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(configDir, "safety-backups", "claude", time.Now().UTC().Format("20060102-150405.000000000"))
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}

	manifest := claudeSafetyBackupManifest{
		Kind:      "vibeswap.claude_safety_backup",
		Reason:    reason,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	manifest.Sources = append(manifest.Sources, backupClaudePath("claude_cli", config.ExpandPath("~/.claude"), filepath.Join(root, "claude_cli")))

	desktopRoot := config.ExpandPath("~/Library/Application Support/Claude")
	desktopDest := filepath.Join(root, "claude_desktop")
	for _, rel := range claudeDesktopCriticalState {
		manifest.Sources = append(manifest.Sources, backupClaudePath(
			filepath.ToSlash(filepath.Join("claude_desktop", rel)),
			filepath.Join(desktopRoot, rel),
			filepath.Join(desktopDest, rel),
		))
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0600); err != nil {
		return "", err
	}
	return root, nil
}

func backupClaudePath(name, src, dst string) claudeSafetyBackupSource {
	source := claudeSafetyBackupSource{Name: name, Source: src, Target: dst}
	resolved, err := filepath.EvalSymlinks(src)
	if err == nil {
		src = resolved
		source.Source = resolved
	}

	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return source
		}
		source.Error = err.Error()
		return source
	}
	source.Exists = true

	if info.IsDir() {
		if _, err := adapter.CloneTree(src, dst, adapter.CloneTreeOptions{SkipNames: claudeSafetyBackupSkipNames()}); err != nil {
			source.Error = err.Error()
		}
		return source
	}
	if !info.Mode().IsRegular() {
		source.Error = "not a regular file or directory"
		return source
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		source.Error = err.Error()
		return source
	}
	if err := adapter.CloneFile(src, dst); err != nil {
		source.Error = err.Error()
	}
	return source
}

func claudeSafetyBackupSkipNames() map[string]struct{} {
	return map[string]struct{}{
		"SingletonLock":   {},
		"SingletonSocket": {},
		"SingletonCookie": {},
		".swap":           {},
	}
}

func ensureClaudeSafetyBackup(targetID, action string) error {
	if targetID != "claude_desktop_oauth" && targetID != "claude_cli" {
		return nil
	}
	if _, err := CreateClaudeSafetyBackup(strings.TrimSpace(action + " " + targetID)); err != nil {
		return fmt.Errorf("failed to create Claude safety backup before %s: %w", action, err)
	}
	return nil
}
