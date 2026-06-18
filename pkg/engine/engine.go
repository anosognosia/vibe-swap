package engine

import (
	"encoding/json"
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/adapter"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ListProfiles returns a map of targetID -> list of profile names.
func ListProfiles() (map[string][]string, error) {
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return nil, err
	}

	result := make(map[string][]string)
	cfg, _ := config.LoadConfig()

	// List subdirectories (targets)
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			targetID := entry.Name()
			targetDir := filepath.Join(profilesDir, targetID)

			files, err := os.ReadDir(targetDir)
			if err != nil {
				continue
			}

			targetType := config.TargetType("")
			if cfg != nil {
				if target, ok := cfg.Targets[targetID]; ok {
					targetType = target.Type
				}
			}

			var profiles []string
			for _, file := range files {
				name := file.Name()
				// Hide the internal "live" dir used by electron_userdata
				// from the profile list (it is the mutable Claude state,
				// not a snapshot).
				if targetType == config.TypeElectronUserdata && name == "live" {
					continue
				}
				if targetID == "claude_cli" && name == ".shared" {
					continue
				}
				if file.IsDir() && targetType == config.TypeSQLite {
					if _, err := os.Stat(filepath.Join(targetDir, name, "cookies.sqlite")); err == nil {
						profiles = append(profiles, name)
					}
				} else if file.IsDir() && !usesJSONProfileFiles(targetType) {
					profiles = append(profiles, name)
				} else if targetType != config.TypeWrappedDir && targetType != config.TypeElectron && targetType != config.TypeSQLite && strings.HasSuffix(name, ".json") {
					if targetType == config.TypeClaudeDesk && !isClaudeDesktopConfigProfile(filepath.Join(targetDir, name)) {
						continue
					}
					profileName := strings.TrimSuffix(name, ".json")
					profiles = append(profiles, profileName)
				}
			}
			result[targetID] = profiles
		}
	}

	return result, nil
}

func usesJSONProfileFiles(targetType config.TargetType) bool {
	switch targetType {
	case config.TypeFile, config.TypeJSONKey, config.TypeKeychain, config.TypeClaudeDesk:
		return true
	default:
		return false
	}
}

func isClaudeDesktopConfigProfile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var profile struct {
		Files map[string]*string `json:"files"`
	}
	if err := json.Unmarshal(data, &profile); err != nil {
		return false
	}
	return len(profile.Files) > 0
}

// SaveProfile saves the active credentials of targetID as profileName.
func SaveProfile(targetID, profileName string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	target, exists := cfg.Targets[targetID]
	if !exists {
		return fmt.Errorf("target not found: %s", targetID)
	}

	adp, err := adapter.GetAdapter(target.Type)
	if err != nil {
		return err
	}

	if !adp.IsInstalled(target) {
		return fmt.Errorf("target %s is not installed or initialized on your system", targetID)
	}

	if err := ensureDesktopAppNotRunning(target, adp, "save"); err != nil {
		return err
	}
	if err := ensureClaudeSafetyBackup(targetID, "save"); err != nil {
		return err
	}

	if err := adp.Save(target, targetID, profileName); err != nil {
		return err
	}

	// Save active state
	state, err := config.LoadActiveState()
	if err == nil {
		state.Targets[targetID] = profileName
		_ = config.SaveActiveState(state)
	}

	return nil
}

// OverwriteProfile replaces an existing saved profile with the current live
// credentials. It saves to a temporary profile first, so process guards and
// other save failures leave the existing profile untouched.
func OverwriteProfile(targetID, profileName string) error {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return fmt.Errorf("profile name cannot be empty")
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	target, exists := cfg.Targets[targetID]
	if !exists {
		return fmt.Errorf("target not found: %s", targetID)
	}

	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return err
	}
	targetDir := filepath.Join(profilesDir, targetID)

	oldPath, _, err := existingProfilePath(targetDir, profileName)
	if err != nil {
		return err
	}

	previousActive := ""
	if state, err := config.LoadActiveState(); err == nil {
		previousActive = state.Targets[targetID]
	}

	tempName, err := uniqueOverwriteTempName(targetDir, profileName)
	if err != nil {
		return err
	}
	if err := SaveProfile(targetID, tempName); err != nil {
		return err
	}

	tempPath, _, err := existingProfilePath(targetDir, tempName)
	if err != nil {
		restoreProfileReference(targetID, target, tempName, previousActive)
		return err
	}

	backupPath, err := uniqueOverwriteBackupPath(targetDir, profileName)
	if err != nil {
		removeProfilePath(tempPath)
		restoreProfileReference(targetID, target, tempName, previousActive)
		return err
	}

	if err := os.Rename(oldPath, backupPath); err != nil {
		removeProfilePath(tempPath)
		restoreProfileReference(targetID, target, tempName, previousActive)
		return fmt.Errorf("failed to stage existing profile for overwrite: %w", err)
	}

	if err := os.Rename(tempPath, oldPath); err != nil {
		_ = os.Rename(backupPath, oldPath)
		removeProfilePath(tempPath)
		restoreProfileReference(targetID, target, tempName, previousActive)
		return fmt.Errorf("failed to replace profile %q: %w", profileName, err)
	}

	if err := os.RemoveAll(backupPath); err != nil {
		_ = setActiveProfileName(targetID, profileName)
		_ = updateElectronUserdataCurrentSnapshot(targetID, target, tempName, profileName)
		return fmt.Errorf("profile %q was overwritten but cleanup failed: %w", profileName, err)
	}

	if err := setActiveProfileName(targetID, profileName); err != nil {
		return err
	}
	if err := updateElectronUserdataCurrentSnapshot(targetID, target, tempName, profileName); err != nil {
		return err
	}

	return nil
}

// SwitchProfile switches targetID to profileName.
func SwitchProfile(targetID, profileName string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	target, exists := cfg.Targets[targetID]
	if !exists {
		return fmt.Errorf("target not found: %s", targetID)
	}

	adp, err := adapter.GetAdapter(target.Type)
	if err != nil {
		return err
	}

	if adp.IsInstalled(target) {
		if err := ensureDesktopAppNotRunning(target, adp, "switch"); err != nil {
			return err
		}
	}
	if err := ensureClaudeSafetyBackup(targetID, "switch"); err != nil {
		return err
	}

	companions, err := switchCompanionProfiles(cfg, targetID, profileName)
	if err != nil {
		return err
	}

	if err := adp.Load(target, targetID, profileName); err != nil {
		return err
	}

	// Update active state
	state, err := config.LoadActiveState()
	if err == nil {
		state.Targets[targetID] = profileName
		for _, companionID := range companions {
			state.Targets[companionID] = profileName
		}
		_ = config.SaveActiveState(state)
	}

	return nil
}

// ClearTargetSession clears a target's live local login/session state without
// deleting saved profiles or shared app data.
func ClearTargetSession(targetID string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	target, exists := cfg.Targets[targetID]
	if !exists {
		return fmt.Errorf("target not found: %s", targetID)
	}

	adp, err := adapter.GetAdapter(target.Type)
	if err != nil {
		return err
	}
	resetter, ok := adp.(adapter.SessionResetter)
	if !ok {
		return fmt.Errorf("target %s does not support clearing live session state", targetID)
	}
	if !adp.IsInstalled(target) {
		return fmt.Errorf("target %s is not installed or initialized on your system", targetID)
	}
	if err := ensureDesktopAppNotRunning(target, adp, "clear session"); err != nil {
		return err
	}
	if err := ensureClaudeSafetyBackup(targetID, "new-login"); err != nil {
		return err
	}
	if err := resetter.ClearSession(target, targetID); err != nil {
		return err
	}

	state, err := config.LoadActiveState()
	if err == nil {
		delete(state.Targets, targetID)
		_ = config.SaveActiveState(state)
	}
	return nil
}

// CloseTargetProcesses asks a target adapter to close known app processes that block safe writes.
func CloseTargetProcesses(targetID string) ([]string, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}

	target, exists := cfg.Targets[targetID]
	if !exists {
		return nil, fmt.Errorf("target not found: %s", targetID)
	}

	adp, err := adapter.GetAdapter(target.Type)
	if err != nil {
		return nil, err
	}

	closer, ok := adp.(adapter.ProcessCloser)
	if !ok {
		return nil, fmt.Errorf("target %s does not support closing blocking processes", targetID)
	}

	return closer.CloseProcesses(target)
}

func ensureDesktopAppNotRunning(target config.Target, adp adapter.Adapter, action string) error {
	guarder, ok := adp.(adapter.ProcessGuarder)
	if !ok {
		return nil
	}
	running := guarder.RunningProcesses(target)
	if len(running) == 0 {
		return nil
	}
	return fmt.Errorf("refusing to %s while desktop app processes are running: %s; quit the desktop app completely and retry", action, strings.Join(running, ", "))
}

func switchCompanionProfiles(cfg *config.Config, targetID, profileName string) ([]string, error) {
	if targetID != "claude_desktop_oauth" {
		return nil, nil
	}

	const companionID = "claude_cli"
	companion, ok := cfg.Targets[companionID]
	if !ok {
		return nil, nil
	}

	profiles, err := ListProfiles()
	if err != nil {
		return nil, err
	}
	if !profileNameExists(profiles[companionID], profileName) {
		return nil, nil
	}

	adp, err := adapter.GetAdapter(companion.Type)
	if err != nil {
		return nil, err
	}
	if err := adp.Load(companion, companionID, profileName); err != nil {
		return nil, fmt.Errorf("failed to switch companion target %s to profile %q: %w", companionID, profileName, err)
	}
	return []string{companionID}, nil
}

func profileNameExists(profiles []string, profileName string) bool {
	for _, p := range profiles {
		if p == profileName {
			return true
		}
	}
	return false
}

// SwitchAllTargets switches all configured and installed targets to the given profile name.
func SwitchAllTargets(profileName string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	profiles, err := ListProfiles()
	if err != nil {
		return err
	}

	type switchCandidate struct {
		targetID string
		target   config.Target
		adp      adapter.Adapter
	}

	var candidates []switchCandidate
	var targetIDs []string
	for targetID := range cfg.Targets {
		targetIDs = append(targetIDs, targetID)
	}
	sort.Strings(targetIDs)

	for _, targetID := range targetIDs {
		target := cfg.Targets[targetID]
		adp, err := adapter.GetAdapter(target.Type)
		if err != nil {
			continue
		}

		if !adp.IsInstalled(target) {
			continue
		}

		if !profileNameExists(profiles[targetID], profileName) {
			continue
		}

		candidates = append(candidates, switchCandidate{targetID: targetID, target: target, adp: adp})
	}

	if len(candidates) == 0 {
		return fmt.Errorf("no targets have a profile named %q to switch to", profileName)
	}

	for _, candidate := range candidates {
		if err := ensureDesktopAppNotRunning(candidate.target, candidate.adp, "switch"); err != nil {
			return err
		}
	}
	for _, candidate := range candidates {
		if err := ensureClaudeSafetyBackup(candidate.targetID, "profile switch"); err != nil {
			return err
		}
	}

	var switched []string
	var failures []string

	for _, candidate := range candidates {
		if err := candidate.adp.Load(candidate.target, candidate.targetID, profileName); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", candidate.targetID, err))
		} else {
			switched = append(switched, candidate.targetID)
		}
	}

	// Update active state for all successfully switched targets
	state, err := config.LoadActiveState()
	if err == nil {
		for _, targetID := range switched {
			state.Targets[targetID] = profileName
		}
		_ = config.SaveActiveState(state)
	}

	if len(failures) > 0 {
		return fmt.Errorf("switched %s but failed for: %s", strings.Join(switched, ", "), strings.Join(failures, ", "))
	}

	return nil
}

// DeleteProfile deletes a saved profile for targetID.
func DeleteProfile(targetID, profileName string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	target, targetExists := cfg.Targets[targetID]

	// Refuse to delete a profile that is currently active on the live system
	// (unless the adapter explicitly opts out via DeleteOverride).
	if targetExists {
		if adp, err := adapter.GetAdapter(target.Type); err == nil {
			if override, ok := adp.(adapter.DeleteOverride); ok {
				if !override.CanDeleteProfile(target, targetID, profileName) {
					return fmt.Errorf("cannot delete profile %q: protected by the adapter", profileName)
				}
			} else if checker, ok := adp.(adapter.ActiveChecker); ok {
				if active, cerr := checker.IsActiveProfile(target, targetID, profileName); cerr == nil && active {
					return fmt.Errorf("cannot delete profile %q: it is the currently active profile for target %s; switch to a different profile first", profileName, targetID)
				}
			}
		}
	}

	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return err
	}

	// Check if directory profile exists (wrapped_dir)
	dirPath := filepath.Join(profilesDir, targetID, profileName)
	fi, err := os.Stat(dirPath)
	if err == nil && fi.IsDir() {
		if err := os.RemoveAll(dirPath); err != nil {
			return fmt.Errorf("failed to delete profile directory: %v", err)
		}
	} else {
		// Check if file profile exists (.json)
		filePath := filepath.Join(profilesDir, targetID, profileName+".json")
		if _, err := os.Stat(filePath); err == nil {
			if err := os.Remove(filePath); err != nil {
				return fmt.Errorf("failed to delete profile file: %v", err)
			}
		} else {
			return fmt.Errorf("profile %s not found for target %s", profileName, targetID)
		}
	}

	// Clean active state if this was the active profile
	state, err := config.LoadActiveState()
	if err == nil {
		if state.Targets[targetID] == profileName {
			delete(state.Targets, targetID)
			_ = config.SaveActiveState(state)

			// If it's a wrapped directory target, we must remove the symlink and create a physical folder
			if targetExists && target.Type == config.TypeWrappedDir && target.Path != "" {
				defaultDir := config.ExpandPath(target.Path)
				_ = os.Remove(defaultDir)         // Remove the symlink
				_ = os.MkdirAll(defaultDir, 0700) // Recreate as a real directory
			}
		}
	}

	return nil
}

// RenameProfile renames a saved profile for targetID and updates active state if needed.
func RenameProfile(targetID, oldName, newName string) error {
	if strings.TrimSpace(oldName) == "" || strings.TrimSpace(newName) == "" {
		return fmt.Errorf("profile names cannot be empty")
	}
	if oldName == newName {
		return fmt.Errorf("new profile name is the same as the old name")
	}

	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return err
	}
	targetDir := filepath.Join(profilesDir, targetID)

	var target config.Target
	targetExists := false

	oldPath, isDir, err := existingProfilePath(targetDir, oldName)
	if err != nil {
		return err
	}

	// Refuse to rename a profile that is currently active on the live system
	// (the live symlink stores the path, not the name).
	if cfg, cfgErr := config.LoadConfig(); cfgErr == nil {
		if configuredTarget, ok := cfg.Targets[targetID]; ok {
			target = configuredTarget
			targetExists = true
			if adp, err := adapter.GetAdapter(target.Type); err == nil {
				if checker, ok := adp.(adapter.ActiveChecker); ok {
					if active, cerr := checker.IsActiveProfile(target, targetID, oldName); cerr == nil && active {
						return fmt.Errorf("cannot rename profile %q: it is the currently active profile for target %s; switch to a different profile first", oldName, targetID)
					}
				}
			}
		}
	}

	if _, _, err := existingProfilePath(targetDir, newName); err == nil {
		return fmt.Errorf("profile %s already exists for target %s", newName, targetID)
	} else if !strings.Contains(err.Error(), "not found") {
		return err
	}

	newPath := filepath.Join(targetDir, newName)
	if !isDir {
		newPath += ".json"
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("failed to rename profile: %v", err)
	}

	if targetExists {
		if err := updateElectronUserdataCurrentSnapshot(targetID, target, oldName, newName); err != nil {
			return err
		}
	}

	state, err := config.LoadActiveState()
	if err == nil && state.Targets[targetID] == oldName {
		state.Targets[targetID] = newName
		_ = config.SaveActiveState(state)
	}

	return nil
}

func uniqueOverwriteTempName(targetDir, profileName string) (string, error) {
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf(".%s.vibeswap-overwrite-%d-%d", profileName, time.Now().UnixNano(), i)
		if _, _, err := existingProfilePath(targetDir, name); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return name, nil
			}
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate temporary overwrite profile name for %q", profileName)
}

func uniqueOverwriteBackupPath(targetDir, profileName string) (string, error) {
	for i := 0; i < 100; i++ {
		path := filepath.Join(targetDir, fmt.Sprintf(".%s.vibeswap-backup-%d-%d", profileName, time.Now().UnixNano(), i))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate temporary overwrite backup path for %q", profileName)
}

func removeProfilePath(path string) {
	if path != "" {
		_ = os.RemoveAll(path)
	}
}

func restoreProfileReference(targetID string, target config.Target, oldName, restoredName string) {
	_ = setActiveProfileName(targetID, restoredName)
	_ = updateElectronUserdataCurrentSnapshot(targetID, target, oldName, restoredName)
}

func setActiveProfileName(targetID, profileName string) error {
	state, err := config.LoadActiveState()
	if err != nil {
		return err
	}
	if profileName == "" {
		delete(state.Targets, targetID)
	} else {
		state.Targets[targetID] = profileName
	}
	return config.SaveActiveState(state)
}

func updateElectronUserdataCurrentSnapshot(targetID string, target config.Target, oldName, newName string) error {
	if target.Type != config.TypeElectronUserdata {
		return nil
	}
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return err
	}
	currentPath := filepath.Join(profilesDir, targetID, ".current")
	data, err := os.ReadFile(currentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(string(data)) != oldName {
		return nil
	}
	return os.WriteFile(currentPath, []byte(newName+"\n"), 0600)
}

func existingProfilePath(targetDir, profileName string) (string, bool, error) {
	dirPath := filepath.Join(targetDir, profileName)
	if fi, err := os.Stat(dirPath); err == nil && fi.IsDir() {
		return dirPath, true, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", false, err
	}

	filePath := filepath.Join(targetDir, profileName+".json")
	if fi, err := os.Stat(filePath); err == nil && !fi.IsDir() {
		return filePath, false, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", false, err
	}

	return "", false, fmt.Errorf("profile %s not found", profileName)
}
