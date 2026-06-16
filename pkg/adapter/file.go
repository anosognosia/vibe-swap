package adapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"vibeswap/pkg/config"
)

type FileAdapter struct{}

type MultiFileProfile struct {
	Files map[string]string `json:"files"` // unexpanded path -> base64 content
}

func (f *FileAdapter) getProfilePath(targetID, profileName string) (string, error) {
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

func (f *FileAdapter) Save(target config.Target, targetID string, profileName string) error {
	destPath, err := f.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	if len(target.Paths) > 0 {
		// Multi-file profile
		prof := MultiFileProfile{Files: make(map[string]string)}
		for _, p := range target.Paths {
			expanded := config.ExpandPath(p)
			data, err := os.ReadFile(expanded)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %v", p, err)
			}
			prof.Files[p] = base64.StdEncoding.EncodeToString(data)
		}
		data, err := json.MarshalIndent(prof, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0600)
	}

	// Single-file profile
	srcPath := config.ExpandPath(target.Path)
	return copyFile(srcPath, destPath)
}

func (f *FileAdapter) Load(target config.Target, targetID string, profileName string) error {
	srcPath, err := f.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	if len(target.Paths) > 0 {
		// Multi-file profile
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		var prof MultiFileProfile
		if err := json.Unmarshal(data, &prof); err != nil {
			return err
		}
		for p, b64Content := range prof.Files {
			decoded, err := base64.StdEncoding.DecodeString(b64Content)
			if err != nil {
				return fmt.Errorf("failed to decode base64 for %s: %v", p, err)
			}
			expanded := config.ExpandPath(p)
			if err := os.MkdirAll(filepath.Dir(expanded), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(expanded, decoded, 0600); err != nil {
				return fmt.Errorf("failed to write file %s: %v", p, err)
			}
		}
		return nil
	}

	// Single-file profile
	destPath := config.ExpandPath(target.Path)
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	return copyFile(srcPath, destPath)
}

func (f *FileAdapter) IsInstalled(target config.Target) bool {
	if len(target.Paths) > 0 {
		for _, p := range target.Paths {
			expanded := config.ExpandPath(p)
			if _, err := os.Stat(expanded); err != nil {
				return false
			}
		}
		return true
	}
	path := config.ExpandPath(target.Path)
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Ensure destination permissions are restricted
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
