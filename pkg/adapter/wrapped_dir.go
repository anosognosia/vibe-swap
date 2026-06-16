package adapter

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		// Source and destination point to the exact same physical folder, so saving is a no-op.
		return nil
	}

	// Use evaluated source path to walk/copy correctly.
	if errSrc == nil {
		srcDir = canonicalSrc
	}

	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		// If the source directory doesn't exist yet, we just create an empty destination directory.
		return os.MkdirAll(destDir, 0700)
	}

	// Clean destination directory before copying
	_ = os.RemoveAll(destDir)
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return err
	}

	return copyDir(srcDir, destDir)
}

func (w *WrappedDirAdapter) Load(target config.Target, targetID string, profileName string) error {
	profilePath, err := w.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return fmt.Errorf("profile directory %s does not exist", profileName)
	}

	// Check if default folder exists as a physical folder and has not been backed up
	defaultDir := config.ExpandPath(target.Path)
	fi, err := os.Lstat(defaultDir)
	if err == nil {
		// If it's a real directory and not a symlink, let's back it up as the "default" profile
		// so the user does not lose their current untracked configuration.
		isSymlink := fi.Mode()&os.ModeSymlink != 0
		if !isSymlink && fi.IsDir() {
			profilesDir, _ := config.GetProfilesDir()
			backupPath := filepath.Join(profilesDir, targetID, "default")
			if _, statErr := os.Stat(backupPath); os.IsNotExist(statErr) {
				_ = os.MkdirAll(backupPath, 0700)
				_ = copyDir(defaultDir, backupPath)
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
