package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"vibeswap/pkg/adapter"
	"vibeswap/pkg/config"
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

	var switched []string
	var failures []string

	for targetID, target := range cfg.Targets {
		adp, err := adapter.GetAdapter(target.Type)
		if err != nil {
			continue
		}

		if !adp.IsInstalled(target) {
			continue
		}

		// Check if profile exists for this target
		profileExists := false
		for _, p := range profiles[targetID] {
			if p == profileName {
				profileExists = true
				break
			}
		}

		if !profileExists {
			continue
		}

		if err := adp.Load(target, targetID, profileName); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", targetID, err))
		} else {
			switched = append(switched, targetID)
		}
	}

	if len(switched) == 0 && len(failures) == 0 {
		return fmt.Errorf("no targets have a profile named %q to switch to", profileName)
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

	oldPath, isDir, err := existingProfilePath(targetDir, oldName)
	if err != nil {
		return err
	}

	// Refuse to rename a profile that is currently active on the live system
	// (the live symlink stores the path, not the name).
	if cfg, cfgErr := config.LoadConfig(); cfgErr == nil {
		if target, ok := cfg.Targets[targetID]; ok {
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

	state, err := config.LoadActiveState()
	if err == nil && state.Targets[targetID] == oldName {
		state.Targets[targetID] = newName
		_ = config.SaveActiveState(state)
	}

	return nil
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
