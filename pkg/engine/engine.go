package engine

import (
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

			var profiles []string
			for _, file := range files {
				if file.IsDir() {
					profiles = append(profiles, file.Name())
				} else if strings.HasSuffix(file.Name(), ".json") {
					profileName := strings.TrimSuffix(file.Name(), ".json")
					profiles = append(profiles, profileName)
				}
			}
			result[targetID] = profiles
		}
	}

	return result, nil
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

	if err := adp.Load(target, targetID, profileName); err != nil {
		return err
	}

	// Update active state
	state, err := config.LoadActiveState()
	if err == nil {
		state.Targets[targetID] = profileName
		_ = config.SaveActiveState(state)
	}

	return nil
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
				_ = os.Remove(defaultDir)          // Remove the symlink
				_ = os.MkdirAll(defaultDir, 0700)   // Recreate as a real directory
			}
		}
	}

	return nil
}
