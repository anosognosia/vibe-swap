package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"vibeswap/pkg/config"
)

type ElectronAdapter struct{}

type electronProfile struct {
	Root          string                      `json:"root"`
	Paths         []string                    `json:"paths"`
	KeychainItems map[string]electronKeychain `json:"keychain_items,omitempty"`
}

type electronKeychain struct {
	Service string `json:"service"`
	Account string `json:"account"`
	Token   string `json:"token"`
}

func (e *ElectronAdapter) getProfilePath(targetID, profileName string) (string, error) {
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

func (e *ElectronAdapter) Save(target config.Target, targetID string, profileName string) error {
	if running := e.runningProcesses(target); len(running) > 0 {
		return fmt.Errorf("refusing to save while desktop app processes are running: %s; quit the desktop app completely and retry", strings.Join(running, ", "))
	}

	profilePath, err := e.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	root := config.ExpandPath(target.Path)
	filesDir := filepath.Join(profilePath, "files")
	if err := os.MkdirAll(filesDir, 0700); err != nil {
		return err
	}

	prof := electronProfile{
		Root:          target.Path,
		Paths:         make([]string, 0, len(target.Paths)),
		KeychainItems: make(map[string]electronKeychain),
	}

	for _, configuredPath := range target.Paths {
		src := config.ExpandPath(configuredPath)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("failed to stat %s: %w", configuredPath, err)
		}

		rel, err := electronRelPath(root, src)
		if err != nil {
			return err
		}
		dst := filepath.Join(filesDir, rel)
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := syncDir(src, dst); err != nil {
				return fmt.Errorf("failed to save directory %s: %w", configuredPath, err)
			}
		} else {
			if err := copyFileHelper(src, dst); err != nil {
				return fmt.Errorf("failed to save file %s: %w", configuredPath, err)
			}
		}
		prof.Paths = append(prof.Paths, configuredPath)
	}

	for _, item := range target.KeychainItems {
		token, err := e.readKeychain(item.Service, item.Account)
		if err != nil {
			return fmt.Errorf("failed to read keychain item %s/%s: %w", item.Service, item.Account, err)
		}
		key := item.Service + "\x00" + item.Account
		prof.KeychainItems[key] = electronKeychain{
			Service: item.Service,
			Account: item.Account,
			Token:   token,
		}
	}

	if len(prof.Paths) == 0 && len(prof.KeychainItems) == 0 {
		return fmt.Errorf("no desktop auth state found to save for target %s", targetID)
	}

	data, err := json.MarshalIndent(prof, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(profilePath, "profile.json"), data, 0600)
}

func (e *ElectronAdapter) Load(target config.Target, targetID string, profileName string) error {
	if running := e.runningProcesses(target); len(running) > 0 {
		return fmt.Errorf("refusing to switch while desktop app processes are running: %s; quit the desktop app completely and retry", strings.Join(running, ", "))
	}

	profilePath, err := e.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(filepath.Join(profilePath, "profile.json"))
	if err != nil {
		return err
	}

	var prof electronProfile
	if err := json.Unmarshal(data, &prof); err != nil {
		return err
	}

	root := config.ExpandPath(target.Path)
	filesDir := filepath.Join(profilePath, "files")
	profilePaths := make(map[string]struct{}, len(prof.Paths))
	for _, configuredPath := range prof.Paths {
		profilePaths[configuredPath] = struct{}{}
	}

	for _, configuredPath := range target.Paths {
		if _, ok := profilePaths[configuredPath]; ok {
			continue
		}
		dst := config.ExpandPath(configuredPath)
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("failed to remove stale desktop session item %s: %w", configuredPath, err)
		}
	}

	for _, configuredPath := range prof.Paths {
		dst := config.ExpandPath(configuredPath)
		rel, err := electronRelPath(root, dst)
		if err != nil {
			return err
		}
		src := filepath.Join(filesDir, rel)
		info, err := os.Stat(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := syncDir(src, dst); err != nil {
				return fmt.Errorf("failed to restore directory %s: %w", configuredPath, err)
			}
		} else {
			if err := copyFileHelper(src, dst); err != nil {
				return fmt.Errorf("failed to restore file %s: %w", configuredPath, err)
			}
		}
	}

	for _, item := range prof.KeychainItems {
		if err := e.writeKeychain(item.Service, item.Account, item.Token); err != nil {
			return fmt.Errorf("failed to write keychain item %s/%s: %w", item.Service, item.Account, err)
		}
	}

	return nil
}

func (e *ElectronAdapter) IsInstalled(target config.Target) bool {
	if target.Path != "" {
		if _, err := os.Stat(config.ExpandPath(target.Path)); err == nil {
			return true
		}
	}
	for _, path := range target.Paths {
		if _, err := os.Stat(config.ExpandPath(path)); err == nil {
			return true
		}
	}
	for _, item := range target.KeychainItems {
		if _, err := e.readKeychain(item.Service, item.Account); err == nil {
			return true
		}
	}
	return false
}

func (e *ElectronAdapter) runningProcesses(target config.Target) []string {
	var running []string
	seen := make(map[string]struct{})

	if len(target.ProcessPatterns) > 0 {
		for _, proc := range e.processesForPatterns(target.ProcessPatterns) {
			label := "matching " + proc.Pattern
			if _, ok := seen[label]; !ok {
				running = append(running, label)
				seen[label] = struct{}{}
			}
		}
		return running
	}

	for _, name := range target.Processes {
		if name == "" {
			continue
		}
		cmd := exec.Command("pgrep", "-x", name)
		if err := cmd.Run(); err == nil {
			if _, ok := seen[name]; !ok {
				running = append(running, name)
				seen[name] = struct{}{}
			}
		}
	}
	return running
}

func (e *ElectronAdapter) CloseProcesses(target config.Target) ([]string, error) {
	if len(target.ProcessPatterns) == 0 {
		return nil, fmt.Errorf("no closeable desktop process patterns configured")
	}

	processes := e.processesForPatterns(target.ProcessPatterns)
	if len(processes) == 0 {
		return nil, nil
	}

	currentPID := strconv.Itoa(os.Getpid())
	var closed []string
	var failures []string
	for _, proc := range processes {
		if proc.PID == "" || proc.PID == currentPID {
			continue
		}
		if err := exec.Command("kill", proc.PID).Run(); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", proc.PID, err))
			continue
		}
		closed = append(closed, fmt.Sprintf("%s matching %s", proc.PID, proc.Pattern))
	}

	if len(failures) > 0 {
		return closed, fmt.Errorf("failed to close desktop processes: %s", strings.Join(failures, ", "))
	}

	time.Sleep(500 * time.Millisecond)
	return closed, nil
}

type desktopProcess struct {
	PID     string
	Pattern string
}

func (e *ElectronAdapter) processesForPatterns(patterns []string) []desktopProcess {
	var processes []desktopProcess
	seen := make(map[string]struct{})
	for _, pattern := range patterns {
		pattern = config.ExpandPath(pattern)
		if pattern == "" {
			continue
		}
		out, err := exec.Command("pgrep", "-f", pattern).Output()
		if err != nil {
			continue
		}
		for _, pid := range strings.Fields(string(out)) {
			if pid == "" {
				continue
			}
			key := pid + "\x00" + pattern
			if _, ok := seen[key]; ok {
				continue
			}
			processes = append(processes, desktopProcess{PID: pid, Pattern: pattern})
			seen[key] = struct{}{}
		}
	}
	return processes
}

func (e *ElectronAdapter) readKeychain(service, account string) (string, error) {
	args := []string{"find-generic-password", "-w", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	cmd := exec.Command("security", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (e *ElectronAdapter) writeKeychain(service, account, token string) error {
	deleteArgs := []string{"delete-generic-password", "-s", service}
	if account != "" {
		deleteArgs = append(deleteArgs, "-a", account)
	}
	_ = exec.Command("security", deleteArgs...).Run()

	addArgs := []string{"add-generic-password", "-U", "-s", service}
	if account != "" {
		addArgs = append(addArgs, "-a", account)
	}
	addArgs = append(addArgs, "-w", token)
	cmd := exec.Command("security", addArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.New(strings.TrimSpace(stderr.String()))
	}
	return nil
}

func electronRelPath(root, path string) (string, error) {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
		return rel, nil
	}
	volume := filepath.VolumeName(path)
	path = strings.TrimPrefix(path, volume)
	path = strings.TrimPrefix(path, string(os.PathSeparator))
	if path == "" {
		return "", fmt.Errorf("cannot derive profile path for %s", root)
	}
	return filepath.Join("_external", path), nil
}
