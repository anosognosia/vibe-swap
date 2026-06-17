package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ElectronUserdataAdapter switches Electron/Chromium auth/session state inside
// a managed "userData" directory (e.g. ~/Library/Application Support/Claude)
// by snapshotting the session-bearing files with APFS copy-on-write clones.
//
// Storage layout:
//
//	~/.config/vibeswap/profiles/<targetID>/
//	  live/             — mutable; the symlink at the userData path points here
//	  <profileName>/    — immutable CoW snapshot of auth/session items
//	  .current          — text file: name of the session snapshot live/ was last loaded from
//
// This separation is important: in earlier designs, the userData symlink
// pointed directly at a snapshot, which meant any sign-out / sign-in Claude
// performed would silently overwrite the "active" snapshot. With a dedicated
// live/ dir, snapshots are never written to after Save returns.
type ElectronUserdataAdapter struct {
	*ElectronAdapter
}

// electronUserdataProfile is a small metadata file stored INSIDE each snapshot
// directory. It is ignored by the Electron app (which only reads its own
// internal files) but lets us validate the snapshot at load time.
type electronUserdataProfile struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`       // unexpanded path the userData was cloned from at save time
	SnapshotOf  string `json:"snapshot_of"`  // name of the snapshot live/ was based on at save time, or "" for first save
	SymlinkPath string `json:"symlink_path"` // unexpanded path the live symlink is expected to be at
	CreatedAt   string `json:"created_at"`
	VibeSwap    string `json:"vibeswap"`
}

const (
	electronUserdataProfileKind = "electron_userdata"
	electronUserdataProfileFile = ".vibeswap-profile.json"
	electronUserdataLiveName    = "live"
	electronUserdataCurrentFile = ".current"
)

var electronUserdataSessionFiles = []string{
	"config.json",
	"Preferences",
	"DIPS",
	"DIPS-wal",
	"SharedStorage",
	"SharedStorage-wal",
	"ant-did",
	"Cookies",
	"Cookies-journal",
	"Network Persistent State",
}

var electronUserdataSessionDirs = []string{
	"Local Storage",
	"Session Storage",
	"Network",
	"IndexedDB",
	"WebStorage",
}

// ResolveSymlinkTarget returns the absolute path of the live userData
// symlink. This is the only required field on the target config.
func (e *ElectronUserdataAdapter) ResolveSymlinkTarget(target config.Target) (string, error) {
	if target.SymlinkTarget == "" {
		return "", fmt.Errorf("target %q is missing symlink_target", target.Name)
	}
	return config.ExpandPath(target.SymlinkTarget), nil
}

// targetProfilesDir returns the directory containing live/, snapshots/, etc.
// for a given targetID.
func (e *ElectronUserdataAdapter) targetProfilesDir(targetID string) (string, error) {
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(profilesDir, targetID), nil
}

// liveDir is the mutable directory the userData symlink points at.
func (e *ElectronUserdataAdapter) liveDir(targetID string) (string, error) {
	d, err := e.targetProfilesDir(targetID)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, electronUserdataLiveName), nil
}

// getSnapshotDir returns the directory holding a CoW snapshot.
func (e *ElectronUserdataAdapter) getSnapshotDir(targetID, profileName string) (string, error) {
	d, err := e.targetProfilesDir(targetID)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, profileName), nil
}

// currentSnapshotName returns the name of the snapshot that the live/ session
// state was last saved from or loaded from (empty if unknown / never set).
func (e *ElectronUserdataAdapter) currentSnapshotName(targetID string) (string, error) {
	d, err := e.targetProfilesDir(targetID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(d, electronUserdataCurrentFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// writeCurrentSnapshotName records which snapshot the live/ session state is based on.
func (e *ElectronUserdataAdapter) writeCurrentSnapshotName(targetID, name string) error {
	d, err := e.targetProfilesDir(targetID)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, electronUserdataCurrentFile), []byte(name+"\n"), 0600)
}

// ensureLiveSymlink makes sure the userData symlink points at the live/ dir.
//
// Three starting states are handled:
//   - Path absent: nothing to migrate, just create the symlink.
//   - Real directory: move it into live/ as a safety backup, then create the
//     symlink (first run, or after the user wiped the profile store).
//   - Existing symlink (correct target): no-op.
//   - Existing symlink (wrong target): migrate the old profile into live/,
//     re-point the symlink, and record the source snapshot name.
func (e *ElectronUserdataAdapter) ensureLiveSymlink(target config.Target, targetID string) error {
	symlinkPath, err := e.ResolveSymlinkTarget(target)
	if err != nil {
		return err
	}
	liveDir, err := e.liveDir(targetID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(liveDir, 0700); err != nil {
		return err
	}

	li, lerr := os.Lstat(symlinkPath)

	// Path is missing entirely. Create the symlink; live/ is already empty.
	if lerr != nil && os.IsNotExist(lerr) {
		return os.Symlink(liveDir, symlinkPath)
	}
	if lerr != nil {
		return lerr
	}

	// It's a symlink.
	if li.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(symlinkPath)
		if err != nil {
			return fmt.Errorf("could not resolve existing symlink at %s: %w", symlinkPath, err)
		}
		if same, _ := samePath(resolved, liveDir); same {
			return nil
		}
		// Old layout: symlink pointed at a snapshot dir. Migrate by
		// copying into live/ and re-pointing the symlink.
		if _, err := CloneTree(resolved, liveDir, CloneTreeOptions{}); err != nil {
			return fmt.Errorf("could not migrate old profile into live/: %w", err)
		}
		if err := os.Remove(symlinkPath); err != nil {
			return err
		}
		if err := os.Symlink(liveDir, symlinkPath); err != nil {
			return fmt.Errorf("could not re-create symlink: %w", err)
		}
		oldName := filepath.Base(resolved)
		if oldName != "" && oldName != "." && oldName != "/" {
			_ = e.writeCurrentSnapshotName(targetID, oldName)
		}
		return nil
	}

	// It's a real directory (first run, or after manual teardown). Move
	// it aside as a safety backup, then copy its contents into live/.
	if li.IsDir() {
		back := fmt.Sprintf("%s.real-bak-%d", symlinkPath, time.Now().UnixNano())
		if err := os.Rename(symlinkPath, back); err != nil {
			return fmt.Errorf("could not move real userData aside: %w", err)
		}
		if _, err := CloneTree(back, liveDir, CloneTreeOptions{}); err != nil {
			_ = os.Rename(back, symlinkPath) // best-effort restore
			return fmt.Errorf("could not seed live/: %w", err)
		}
		_ = os.RemoveAll(back)
		return os.Symlink(liveDir, symlinkPath)
	}

	return fmt.Errorf("%s exists but is neither a symlink nor a directory", symlinkPath)
}

// Save takes a CoW snapshot of the current live/ auth/session state. The
// live/ directory is NOT modified, and the userData symlink is NOT swapped, so
// any subsequent Claude sign-in/sign-out activity goes into live/ rather than
// overwriting the new snapshot.
func (e *ElectronUserdataAdapter) Save(target config.Target, targetID, profileName string) error {
	if running := e.runningProcesses(target); len(running) > 0 {
		return fmt.Errorf("refusing to save while desktop app processes are running: %s; quit the desktop app completely and retry", strings.Join(running, ", "))
	}

	if profileName == electronUserdataLiveName {
		return fmt.Errorf("profile name %q is reserved for the live directory", electronUserdataLiveName)
	}

	if err := e.ensureLiveSymlink(target, targetID); err != nil {
		return err
	}

	liveDir, err := e.liveDir(targetID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(liveDir); err != nil {
		return fmt.Errorf("live dir %s not found: %w", liveDir, err)
	}

	snapshotDir, err := e.getSnapshotDir(targetID, profileName)
	if err != nil {
		return err
	}
	if isNonEmptyDir(snapshotDir) {
		return fmt.Errorf("snapshot %q already exists for target %s; delete it first or pick a new name", profileName, targetID)
	}

	// Remove the empty dir we created in getSnapshotDir so CloneTree starts clean.
	if err := os.Remove(snapshotDir); err != nil && !os.IsNotExist(err) {
		return err
	}

	copied, err := cloneElectronSessionItems(liveDir, snapshotDir)
	if err != nil {
		_ = os.RemoveAll(snapshotDir)
		return fmt.Errorf("failed to snapshot desktop session state: %w", err)
	}
	if copied == 0 {
		_ = os.RemoveAll(snapshotDir)
		return fmt.Errorf("no desktop auth/session state found to save for target %s", targetID)
	}

	// Record which snapshot live/ was based on at save time.
	current, _ := e.currentSnapshotName(targetID)

	meta := electronUserdataProfile{
		Kind:        electronUserdataProfileKind,
		Source:      target.SymlinkTarget,
		SnapshotOf:  current,
		SymlinkPath: target.SymlinkTarget,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		VibeSwap:    "vibeswap electron_userdata",
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, electronUserdataProfileFile), metaBytes, 0600); err != nil {
		return err
	}

	if err := e.warnIfDuplicateSessionKey(snapshotDir, targetID, profileName); err != nil {
		_ = os.RemoveAll(snapshotDir)
		return err
	}

	return e.writeCurrentSnapshotName(targetID, profileName)
}

// Load copies a snapshot's auth/session items into live/, overwriting only the
// session items Claude uses for login. The userData symlink is NOT swapped (it
// always points at live/), and shared heavy app data such as vm_bundles remains
// in live/.
//
// Any unsaved auth/session changes in live/ are lost. The caller is expected
// to have saved them first if they matter.
func (e *ElectronUserdataAdapter) Load(target config.Target, targetID, profileName string) error {
	if running := e.runningProcesses(target); len(running) > 0 {
		return fmt.Errorf("refusing to switch while desktop app processes are running: %s; quit the desktop app completely and retry", strings.Join(running, ", "))
	}

	if profileName == electronUserdataLiveName {
		return fmt.Errorf("profile name %q is reserved for the live directory", electronUserdataLiveName)
	}

	if err := e.ensureLiveSymlink(target, targetID); err != nil {
		return err
	}

	snapshotDir, err := e.getSnapshotDir(targetID, profileName)
	if err != nil {
		return err
	}
	if err := e.validateSnapshot(snapshotDir); err != nil {
		return err
	}

	liveDir, err := e.liveDir(targetID)
	if err != nil {
		return err
	}

	if err := clearElectronSessionItems(liveDir); err != nil {
		return fmt.Errorf("could not clear live session state: %w", err)
	}
	copied, err := cloneElectronSessionItems(snapshotDir, liveDir)
	if err != nil {
		return fmt.Errorf("could not clone snapshot session state into live/: %w", err)
	}
	if copied == 0 {
		return fmt.Errorf("snapshot %q does not contain desktop auth/session state", profileName)
	}

	if err := e.writeCurrentSnapshotName(targetID, profileName); err != nil {
		return err
	}
	return nil
}

// ClearSession removes the live auth/session state without using Claude's
// in-app logout. The next Claude launch should show the login screen while
// preserving shared app data like vm_bundles and MCP configuration.
func (e *ElectronUserdataAdapter) ClearSession(target config.Target, targetID string) error {
	if running := e.runningProcesses(target); len(running) > 0 {
		return fmt.Errorf("refusing to clear session while desktop app processes are running: %s; quit the desktop app completely and retry", strings.Join(running, ", "))
	}

	if err := e.ensureLiveSymlink(target, targetID); err != nil {
		return err
	}
	liveDir, err := e.liveDir(targetID)
	if err != nil {
		return err
	}
	if err := clearElectronSessionItems(liveDir); err != nil {
		return err
	}
	return e.writeCurrentSnapshotName(targetID, "")
}

func (e *ElectronUserdataAdapter) validateSnapshot(snapshotDir string) error {
	metaPath := filepath.Join(snapshotDir, electronUserdataProfileFile)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("snapshot %s is missing the %s marker: %w", filepath.Base(snapshotDir), electronUserdataProfileFile, err)
	}
	var meta electronUserdataProfile
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("snapshot %s has an invalid %s marker: %w", filepath.Base(snapshotDir), electronUserdataProfileFile, err)
	}
	if meta.Kind != electronUserdataProfileKind {
		return fmt.Errorf("snapshot %s has kind=%q, want %q", filepath.Base(snapshotDir), meta.Kind, electronUserdataProfileKind)
	}
	return nil
}

// IsInstalled reports whether the live userData symlink is present and points
// at a valid live/ dir.
func (e *ElectronUserdataAdapter) IsInstalled(target config.Target) bool {
	symlinkPath, err := e.ResolveSymlinkTarget(target)
	if err != nil {
		return false
	}
	_, err = os.Lstat(symlinkPath)
	return err == nil
}

// IsActiveProfile reports whether the named snapshot is the one live/ was
// most recently loaded from (or saved from, in the new design).
func (e *ElectronUserdataAdapter) IsActiveProfile(target config.Target, targetID, profileName string) (bool, error) {
	if profileName == electronUserdataLiveName {
		return false, nil
	}
	current, err := e.currentSnapshotName(targetID)
	if err != nil {
		return false, err
	}
	return current == profileName, nil
}

// CanDeleteProfile returns true for electron_userdata. In the new design,
// the userData symlink always points at live/ (a real directory), not at a
// snapshot, so deleting an "active" snapshot does not leave the system in
// an unrecoverable state: the live/ data is intact and the snapshot can
// be re-created with another save. The .current pointer may briefly
// reference the deleted name, but the next save rewrites it.
func (e *ElectronUserdataAdapter) CanDeleteProfile(_ config.Target, _, profileName string) bool {
	return profileName != electronUserdataLiveName
}

func (e *ElectronUserdataAdapter) RunningProcesses(target config.Target) []string {
	return (&ElectronAdapter{}).runningProcesses(target)
}

// warnIfDuplicateSessionKey checks the new snapshot's sessionKey cookie
// against all other snapshots for the same target. If a match is found, it
// returns an error so the caller can decide whether to fail the save or
// just warn.
func (e *ElectronUserdataAdapter) warnIfDuplicateSessionKey(snapshotDir, targetID, profileName string) error {
	newKey, err := readSessionKeyCiphertext(snapshotDir)
	if err != nil {
		return nil // no cookies; nothing to compare
	}
	if newKey == "" {
		return nil
	}

	d, err := e.targetProfilesDir(targetID)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == electronUserdataLiveName || entry.Name() == profileName {
			continue
		}
		other, err := readSessionKeyCiphertext(filepath.Join(d, entry.Name()))
		if err != nil || other == "" {
			continue
		}
		if other == newKey {
			return fmt.Errorf("warning: this snapshot's sessionKey cookie matches snapshot %q — they appear to be the same Claude account; if you intended a different account, save the current profile, run `vibeswap new-login claude_desktop_oauth`, sign in to the new account, then save again", entry.Name())
		}
	}
	return nil
}

// readSessionKeyCiphertext returns the hex-encoded encrypted_value of the
// sessionKey cookie in the given userData directory's Cookies SQLite file,
// or "" if no cookie is found.
func readSessionKeyCiphertext(userDataDir string) (string, error) {
	cookiesPath := filepath.Join(userDataDir, "Cookies")
	if _, err := os.Stat(cookiesPath); err != nil {
		return "", nil
	}
	// Use the system sqlite3 CLI; the SQLite adapter already depends on it.
	out, err := sqliteOutput(cookiesPath, "SELECT hex(encrypted_value) FROM cookies WHERE name='sessionKey' AND host_key LIKE '%claude.ai' LIMIT 1;")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

func cloneElectronSessionItems(srcRoot, dstRoot string) (int, error) {
	if err := os.MkdirAll(dstRoot, 0700); err != nil {
		return 0, err
	}

	copied := 0
	for _, name := range electronUserdataSessionFiles {
		ok, err := cloneElectronSessionItem(filepath.Join(srcRoot, name), filepath.Join(dstRoot, name))
		if err != nil {
			return copied, err
		}
		if ok {
			copied++
		}
	}
	for _, name := range electronUserdataSessionDirs {
		ok, err := cloneElectronSessionItem(filepath.Join(srcRoot, name), filepath.Join(dstRoot, name))
		if err != nil {
			return copied, err
		}
		if ok {
			copied++
		}
	}
	return copied, nil
}

func cloneElectronSessionItem(src, dst string) (bool, error) {
	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := os.RemoveAll(dst); err != nil {
		return false, err
	}
	if info.IsDir() {
		_, err := CloneTree(src, dst, CloneTreeOptions{
			SkipNames: map[string]struct{}{
				"SingletonLock":             {},
				"SingletonSocket":           {},
				"SingletonCookie":           {},
				".swap":                     {},
				electronUserdataProfileFile: {},
				electronUserdataCurrentFile: {},
			},
		})
		return true, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return false, err
	}
	return true, CloneFile(src, dst)
}

func clearElectronSessionItems(root string) error {
	for _, name := range electronUserdataSessionFiles {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	for _, name := range electronUserdataSessionDirs {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}

// sqliteOutput runs `sqlite3 -batch dbPath sql` and returns stdout. Returns
// an error if sqlite3 is not installed or the query fails.
func sqliteOutput(dbPath, sql string) (string, error) {
	cmd := exec.Command("sqlite3", "-batch", dbPath, sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}

func samePath(a, b string) (bool, error) {
	aa, err := canonicalPath(a)
	if err != nil {
		return false, err
	}
	bb, err := canonicalPath(b)
	if err != nil {
		return false, err
	}
	return aa == bb, nil
}

func canonicalPath(p string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	}
	return filepath.Abs(p)
}

func isNonEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) > 0
}
