package adapter

import (
	"io"
	"os"
	"path/filepath"
	"vibeswap/pkg/config"
)

type FileAdapter struct{}

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
	srcPath := config.ExpandPath(target.Path)
	destPath, err := f.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	return copyFile(srcPath, destPath)
}

func (f *FileAdapter) Load(target config.Target, targetID string, profileName string) error {
	srcPath, err := f.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}
	destPath := config.ExpandPath(target.Path)

	// Ensure the parent directory of destPath exists
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	return copyFile(srcPath, destPath)
}

func (f *FileAdapter) IsInstalled(target config.Target) bool {
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
