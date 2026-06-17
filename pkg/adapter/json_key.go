package adapter

import (
	"encoding/json"
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"os"
	"path/filepath"
	"strings"
)

type JSONKeyAdapter struct{}

type JSONKeyValue struct {
	Value interface{} `json:"value"`
}

func (j *JSONKeyAdapter) getProfilePath(targetID, profileName string) (string, error) {
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

func (j *JSONKeyAdapter) Save(target config.Target, targetID string, profileName string) error {
	filePath := config.ExpandPath(target.Path)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		return err
	}

	val, found := getNestedValue(jsonMap, target.Key)
	if !found {
		return fmt.Errorf("key %s not found in JSON file %s", target.Key, filePath)
	}

	profilePath, err := j.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	profileVal := JSONKeyValue{Value: val}
	valData, err := json.MarshalIndent(profileVal, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(profilePath, valData, 0600)
}

func (j *JSONKeyAdapter) Load(target config.Target, targetID string, profileName string) error {
	profilePath, err := j.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	valData, err := os.ReadFile(profilePath)
	if err != nil {
		return err
	}

	var profileVal JSONKeyValue
	if err := json.Unmarshal(valData, &profileVal); err != nil {
		return err
	}

	filePath := config.ExpandPath(target.Path)
	data, err := os.ReadFile(filePath)
	if err != nil {
		// If file doesn't exist, create an empty JSON object
		if os.IsNotExist(err) {
			data = []byte("{}")
			if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		return err
	}

	if err := setNestedValue(jsonMap, target.Key, profileVal.Value); err != nil {
		return err
	}

	updatedData, err := json.MarshalIndent(jsonMap, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, updatedData, 0600)
}

func (j *JSONKeyAdapter) IsInstalled(target config.Target) bool {
	path := config.ExpandPath(target.Path)
	_, err := os.Stat(path)
	return err == nil
}

func getNestedValue(m map[string]interface{}, keyPath string) (interface{}, bool) {
	parts := strings.Split(keyPath, ".")
	var current interface{} = m
	for _, part := range parts {
		currMap, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		val, exists := currMap[part]
		if !exists {
			return nil, false
		}
		current = val
	}
	return current, true
}

func setNestedValue(m map[string]interface{}, keyPath string, val interface{}) error {
	parts := strings.Split(keyPath, ".")
	var current map[string]interface{} = m
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		next, exists := current[part]
		if !exists {
			nextMap := make(map[string]interface{})
			current[part] = nextMap
			current = nextMap
		} else {
			nextMap, ok := next.(map[string]interface{})
			if !ok {
				return fmt.Errorf("path component %s is not a map", part)
			}
			current = nextMap
		}
	}
	current[parts[len(parts)-1]] = val
	return nil
}
