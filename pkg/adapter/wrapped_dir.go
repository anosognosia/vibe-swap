package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"vibeswap/pkg/config"
)

type WrappedDirAdapter struct{}

func (w *WrappedDirAdapter) getProfilePath(targetID, profileName string) (string, error) {
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return "", err
	}
	targetDir := filepath.Join(profilesDir, targetID, profileName)
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return "", err
	}
	return targetDir, nil
}

func (w *WrappedDirAdapter) Save(target config.Target, targetID string, profileName string) error {
	destDir, err := w.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	// Determine source directory.
	// If no profile is currently active, we copy the default path (e.g. ~/.claude).
	// If a profile IS active, we copy from the active profile's folder.
	state, err := config.LoadActiveState()
	var srcDir string
	if err == nil && state.Targets[targetID] != "" && state.Targets[targetID] != profileName {
		profilesDir, _ := config.GetProfilesDir()
		srcDir = filepath.Join(profilesDir, targetID, state.Targets[targetID])
	} else {
		srcDir = config.ExpandPath(target.Path)
	}

	// Evaluate symlinks to avoid copying a directory onto itself.
	canonicalSrc, errSrc := filepath.EvalSymlinks(srcDir)
	canonicalDst, errDst := filepath.EvalSymlinks(destDir)
	if errSrc == nil && errDst == nil && canonicalSrc == canonicalDst {
		// Source and destination point to the exact same physical folder, so copy is a no-op.
		// But we still want to save the Keychain credential if applicable!
		_ = w.saveKeychain(target, targetID, destDir)
		return nil
	}

	// Use evaluated source path to walk/copy correctly.
	if errSrc == nil {
		srcDir = canonicalSrc
	}

	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		// If the source directory doesn't exist yet, we just create an empty destination directory.
		if err := os.MkdirAll(destDir, 0700); err != nil {
			return err
		}
	} else {
		// Clean destination directory before copying
		_ = os.RemoveAll(destDir)
		if err := os.MkdirAll(destDir, 0700); err != nil {
			return err
		}
		if err := copyDir(srcDir, destDir); err != nil {
			return err
		}
	}

	// Save Keychain credential if configured
	_ = w.saveKeychain(target, targetID, destDir)

	// Create VibeSwap initialization marker so we know the user is active and we shouldn't auto-migrate later
	configDir, err := config.GetConfigDir()
	if err == nil {
		initMarker := filepath.Join(configDir, ".initialized")
		_ = os.WriteFile(initMarker, []byte(""), 0600)
	}

	return nil
}

func (w *WrappedDirAdapter) Load(target config.Target, targetID string, profileName string) error {
	profilePath, err := w.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return fmt.Errorf("profile directory %s does not exist", profileName)
	}

	// Before loading the new profile, save the current active profile's state (including keychain credentials).
	state, err := config.LoadActiveState()
	if err == nil && state.Targets[targetID] != "" && state.Targets[targetID] != profileName {
		activeProfile := state.Targets[targetID]
		activeProfilePath, pathErr := w.getProfilePath(targetID, activeProfile)
		if pathErr == nil {
			_ = w.saveKeychain(target, targetID, activeProfilePath)
		}
	}

	// Check if default folder exists as a physical folder and has not been backed up
	defaultDir := config.ExpandPath(target.Path)
	fi, err := os.Lstat(defaultDir)
	if err == nil {
		// If it's a real directory and not a symlink, let's back it up as the "default" profile
		// so the user does not lose their current untracked configuration.
		isSymlink := fi.Mode()&os.ModeSymlink != 0
		if !isSymlink && fi.IsDir() {
			configDir, _ := config.GetConfigDir()
			initMarker := filepath.Join(configDir, ".initialized")

			// Only back up as "default" if VibeSwap has never been initialized before
			if _, statErr := os.Stat(initMarker); os.IsNotExist(statErr) {
				profilesDir, _ := config.GetProfilesDir()
				backupPath := filepath.Join(profilesDir, targetID, "default")
				if _, backupStatErr := os.Stat(backupPath); os.IsNotExist(backupStatErr) {
					_ = os.MkdirAll(backupPath, 0700)
					_ = copyDir(defaultDir, backupPath)
					_ = w.saveKeychain(target, targetID, backupPath)
				}
				// Create the initialization marker
				_ = os.WriteFile(initMarker, []byte(""), 0600)
			}
			_ = os.RemoveAll(defaultDir)
		} else if isSymlink {
			// If it's a symlink, remove it so we can re-create it pointing to the active profile
			_ = os.Remove(defaultDir)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// Ensure target path is fully cleared of any file/folder/stale symlink
	_ = os.RemoveAll(defaultDir)

	// Now create the symlink pointing to the active profile's folder
	if err := os.Symlink(profilePath, defaultDir); err != nil {
		return fmt.Errorf("failed to create symlink from %s to %s: %w", defaultDir, profilePath, err)
	}

	// Load Keychain credentials if they exist in the loaded profile
	_ = w.loadKeychain(target, targetID, profilePath)

	return nil
}

func (w *WrappedDirAdapter) saveKeychain(target config.Target, targetID string, destDir string) error {
	service := target.Service
	if service == "" && targetID == "claude_cli" {
		service = "Claude Code-credentials"
	}

	if service == "" {
		return nil
	}

	token, err := w.readFromKeychain(service)
	if err != nil {
		return err
	}

	account := target.Account
	if account == "" {
		account = "default"
	}

	kv := keychainValue{
		Account: account,
		Token:   token,
	}

	data, err := json.MarshalIndent(kv, "", "  ")
	if err != nil {
		return err
	}

	keychainFile := filepath.Join(destDir, ".vibeswap_keychain.json")
	return os.WriteFile(keychainFile, data, 0600)
}

func (w *WrappedDirAdapter) loadKeychain(target config.Target, targetID string, profilePath string) error {
	service := target.Service
	if service == "" && targetID == "claude_cli" {
		service = "Claude Code-credentials"
	}

	if service == "" {
		return nil
	}

	keychainFile := filepath.Join(profilePath, ".vibeswap_keychain.json")
	data, err := os.ReadFile(keychainFile)
	if err != nil {
		return err
	}

	var kv keychainValue
	if err := json.Unmarshal(data, &kv); err != nil {
		return err
	}

	return w.writeToKeychain(service, kv.Account, kv.Token)
}

type keychainValue struct {
	Account string `json:"account"`
	Token   string `json:"token"`
}

func (w *WrappedDirAdapter) readFromKeychain(service string) (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-w", "-s", service)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (w *WrappedDirAdapter) writeToKeychain(service, account, token string) error {
	// First delete existing entry (ignore failure)
	deleteCmd := exec.Command("security", "delete-generic-password", "-s", service)
	_ = deleteCmd.Run()

	// Add new entry
	addCmd := exec.Command("security", "add-generic-password", "-a", account, "-s", service, "-w", token)
	var stderr bytes.Buffer
	addCmd.Stderr = &stderr

	err := addCmd.Run()
	if err != nil {
		return errors.New(strings.TrimSpace(stderr.String()))
	}

	return nil
}

func (w *WrappedDirAdapter) IsInstalled(target config.Target) bool {
	// Look for the binary in path
	_, err := exec.LookPath(target.Binary)
	if err == nil {
		return true
	}
	// Or check if the default configuration directory exists
	path := config.ExpandPath(target.Path)
	_, err = os.Stat(path)
	return err == nil
}

// copyDir recursively copies a directory tree, attempting to preserve permissions.
func copyDir(src string, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, targetPath)
		}
		return copyFileHelper(path, targetPath)
	})
}

func copyFileHelper(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
