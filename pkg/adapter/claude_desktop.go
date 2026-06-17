package adapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"os"
	"path/filepath"
)

type ClaudeDesktopAdapter struct{}

type ClaudeDesktopConfigProfile struct {
	Files map[string]*string `json:"files"`
}

func (c *ClaudeDesktopAdapter) getProfilePath(targetID, profileName string) (string, error) {
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return "", err
	}
	targetDir := filepath.Join(profilesDir, targetID)
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(targetDir, profileName+".json"), nil
}

func (c *ClaudeDesktopAdapter) Save(target config.Target, targetID string, profileName string) error {
	paths := claudeDesktopPaths(target)
	if len(paths) == 0 {
		return fmt.Errorf("no Claude Desktop config paths configured for target %s", targetID)
	}

	profile := ClaudeDesktopConfigProfile{Files: make(map[string]*string, len(paths))}
	for _, path := range paths {
		expanded := config.ExpandPath(path)
		data, err := os.ReadFile(expanded)
		if os.IsNotExist(err) {
			profile.Files[path] = nil
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to read Claude Desktop config file %s: %w", path, err)
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		profile.Files[path] = &encoded
	}

	destPath, err := c.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0600)
}

func (c *ClaudeDesktopAdapter) Load(target config.Target, targetID string, profileName string) error {
	srcPath, err := c.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}

	var profile ClaudeDesktopConfigProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return fmt.Errorf("failed to parse Claude Desktop profile %q: %w", profileName, err)
	}
	if len(profile.Files) == 0 {
		return fmt.Errorf("Claude Desktop profile %q does not contain any config files", profileName)
	}

	for path, encoded := range profile.Files {
		expanded := config.ExpandPath(path)
		if encoded == nil {
			if err := os.Remove(expanded); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove Claude Desktop config file %s: %w", path, err)
			}
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(*encoded)
		if err != nil {
			return fmt.Errorf("failed to decode Claude Desktop config file %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(expanded), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(expanded, decoded, 0600); err != nil {
			return fmt.Errorf("failed to write Claude Desktop config file %s: %w", path, err)
		}
	}
	return nil
}

func (c *ClaudeDesktopAdapter) IsInstalled(target config.Target) bool {
	for _, path := range claudeDesktopPaths(target) {
		if _, err := os.Stat(config.ExpandPath(path)); err == nil {
			return true
		}
	}
	if _, err := os.Stat("/Applications/Claude.app"); err == nil {
		return true
	}
	return false
}

func (c *ClaudeDesktopAdapter) CloseProcesses(target config.Target) ([]string, error) {
	return (&ElectronAdapter{}).CloseProcesses(target)
}

func (c *ClaudeDesktopAdapter) RunningProcesses(target config.Target) []string {
	return (&ElectronAdapter{}).runningProcesses(target)
}

func claudeDesktopPaths(target config.Target) []string {
	if len(target.Paths) > 0 {
		return target.Paths
	}
	if target.Path != "" {
		return []string{target.Path}
	}
	return nil
}
