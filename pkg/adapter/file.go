package adapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

	// Resolve all paths to save
	var paths []string
	if len(target.Paths) > 0 {
		paths = target.Paths
	} else if target.Path != "" {
		expanded := config.ExpandPath(target.Path)
		fi, err := os.Stat(expanded)
		if err == nil && fi.IsDir() {
			// Walk directory recursively to get all files
			err = filepath.Walk(expanded, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() {
					home, err := os.UserHomeDir()
					var unexpanded string
					if err == nil && strings.HasPrefix(path, home) {
						unexpanded = "~" + strings.TrimPrefix(path, home)
					} else {
						unexpanded = path
					}
					paths = append(paths, unexpanded)
				}
				return nil
			})
			if err != nil {
				return err
			}
		} else {
			paths = []string{target.Path}
		}
	}

	if len(paths) > 1 {
		// Multi-file profile
		prof := MultiFileProfile{Files: make(map[string]string)}
		for _, p := range paths {
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
	} else if len(paths) == 1 {
		// Single-file profile
		srcPath := config.ExpandPath(paths[0])
		return copyFile(srcPath, destPath)
	}

	return fmt.Errorf("no files found to save for target %s", targetID)
}

func (f *FileAdapter) Load(target config.Target, targetID string, profileName string) error {
	srcPath, err := f.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	// Clean up destination directory if target.Path is a directory
	if target.Path != "" {
		expanded := config.ExpandPath(target.Path)
		fi, err := os.Stat(expanded)
		if err == nil && fi.IsDir() {
			// Clean it up before restoring to avoid mixing profile contents
			os.RemoveAll(expanded)
		}
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}

	// Try to unmarshal as a MultiFileProfile first
	var prof MultiFileProfile
	if err := json.Unmarshal(data, &prof); err == nil && len(prof.Files) > 0 {
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

	// Fallback to legacy single-file profile
	destPath := config.ExpandPath(target.Path)
	if destPath == "" && len(target.Paths) > 0 {
		destPath = config.ExpandPath(target.Paths[0])
	}
	if destPath == "" {
		return fmt.Errorf("no destination path configured for target %s", targetID)
	}
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
