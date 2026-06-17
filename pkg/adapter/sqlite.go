package adapter

import (
	"bytes"
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type SQLiteAdapter struct{}

const savedSQLiteCookiesFile = "cookies.sqlite"
const savedSQLiteFilesDir = "files"

var requiredClaudeDesktopCookies = []string{"sessionKey", "lastActiveOrg"}

func (s *SQLiteAdapter) Save(target config.Target, targetID string, profileName string) error {
	if running := (&ElectronAdapter{}).runningProcesses(target); len(running) > 0 {
		return fmt.Errorf("refusing to save while desktop app processes are running: %s; quit the desktop app completely and retry", strings.Join(running, ", "))
	}

	liveDB := config.ExpandPath(target.Path)
	if _, err := os.Stat(liveDB); err != nil {
		return fmt.Errorf("failed to read SQLite cookie database %s: %w", target.Path, err)
	}

	profilePath, err := s.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}
	savedDB := filepath.Join(profilePath, savedSQLiteCookiesFile)
	_ = os.Remove(savedDB)

	if err := s.runSQLite(liveDB, fmt.Sprintf("VACUUM INTO %s;", sqliteString(savedDB))); err != nil {
		return fmt.Errorf("failed to snapshot SQLite cookie database: %w", err)
	}

	names := cookieNames(target)
	filter := cookieFilterSQL(names)
	if err := s.runSQLite(savedDB, fmt.Sprintf("DELETE FROM cookies WHERE NOT (%s); VACUUM;", filter)); err != nil {
		return fmt.Errorf("failed to filter saved Claude Desktop cookies: %w", err)
	}

	if err := s.validateCookies(savedDB, requiredCookieNames(targetID)); err != nil {
		return err
	}

	if err := s.saveCompanionState(target, profilePath); err != nil {
		return err
	}

	return nil
}

func (s *SQLiteAdapter) Load(target config.Target, targetID string, profileName string) error {
	if running := (&ElectronAdapter{}).runningProcesses(target); len(running) > 0 {
		return fmt.Errorf("refusing to switch while desktop app processes are running: %s; quit the desktop app completely and retry", strings.Join(running, ", "))
	}

	profilePath, err := s.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}
	savedDB := filepath.Join(profilePath, savedSQLiteCookiesFile)
	if _, err := os.Stat(savedDB); err != nil {
		return fmt.Errorf("saved SQLite profile %q not found for target %s", profileName, targetID)
	}
	if err := s.validateCookies(savedDB, requiredCookieNames(targetID)); err != nil {
		return err
	}
	if err := s.restoreCompanionState(target, profilePath); err != nil {
		return err
	}

	liveDB := config.ExpandPath(target.Path)
	liveInfo, err := os.Stat(liveDB)
	if err != nil {
		return fmt.Errorf("failed to read SQLite cookie database %s: %w", target.Path, err)
	}

	tempDir, err := os.MkdirTemp("", "vibeswap-sqlite-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	workingDB := filepath.Join(tempDir, "Cookies")
	if err := s.runSQLite(liveDB, fmt.Sprintf("VACUUM INTO %s;", sqliteString(workingDB))); err != nil {
		return fmt.Errorf("failed to stage live SQLite cookie database: %w", err)
	}

	targetColumns, err := s.cookieColumns(workingDB)
	if err != nil {
		return err
	}
	savedColumns, err := s.cookieColumns(savedDB)
	if err != nil {
		return err
	}
	columns := commonColumns(targetColumns, savedColumns)
	if len(columns) == 0 {
		return fmt.Errorf("saved and live cookie databases do not share cookie columns")
	}

	filter := cookieFilterSQL(cookieNames(target))
	columnSQL := quoteIdentifiers(columns)
	restoreSQL := fmt.Sprintf(`
ATTACH DATABASE %s AS saved;
BEGIN IMMEDIATE;
DELETE FROM cookies WHERE %s;
INSERT INTO cookies (%s)
SELECT %s FROM saved.cookies WHERE %s;
COMMIT;
DETACH DATABASE saved;
`, sqliteString(savedDB), cookieHostSQL(), columnSQL, columnSQL, filter)
	if err := s.runSQLite(workingDB, restoreSQL); err != nil {
		return fmt.Errorf("failed to restore Claude Desktop cookie rows: %w", err)
	}
	if err := s.validateCookies(workingDB, requiredCookieNames(targetID)); err != nil {
		return fmt.Errorf("staged cookie restore did not validate: %w", err)
	}

	if err := copyFileHelper(workingDB, liveDB); err != nil {
		return fmt.Errorf("failed to replace live SQLite cookie database: %w", err)
	}
	_ = os.Chmod(liveDB, liveInfo.Mode())
	s.removeSQLiteSidecars(liveDB)
	return nil
}

func (s *SQLiteAdapter) IsInstalled(target config.Target) bool {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return false
	}
	path := config.ExpandPath(target.Path)
	_, err := os.Stat(path)
	return err == nil
}

func (s *SQLiteAdapter) CloseProcesses(target config.Target) ([]string, error) {
	return (&ElectronAdapter{}).CloseProcesses(target)
}

func (s *SQLiteAdapter) RunningProcesses(target config.Target) []string {
	return (&ElectronAdapter{}).runningProcesses(target)
}

func (s *SQLiteAdapter) getProfilePath(targetID, profileName string) (string, error) {
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

func (s *SQLiteAdapter) saveCompanionState(target config.Target, profilePath string) error {
	if len(target.Paths) == 0 {
		return nil
	}

	filesDir := filepath.Join(profilePath, savedSQLiteFilesDir)
	if err := os.MkdirAll(filesDir, 0700); err != nil {
		return err
	}

	root := sqliteCompanionRoot(target)
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
				return fmt.Errorf("failed to save companion state %s: %w", configuredPath, err)
			}
		} else if err := copyFileHelper(src, dst); err != nil {
			return fmt.Errorf("failed to save companion state %s: %w", configuredPath, err)
		}
	}
	return nil
}

func (s *SQLiteAdapter) restoreCompanionState(target config.Target, profilePath string) error {
	if len(target.Paths) == 0 {
		return nil
	}

	filesDir := filepath.Join(profilePath, savedSQLiteFilesDir)
	if _, err := os.Stat(filesDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	root := sqliteCompanionRoot(target)
	for _, configuredPath := range target.Paths {
		dst := config.ExpandPath(configuredPath)
		rel, err := electronRelPath(root, dst)
		if err != nil {
			return err
		}
		src := filepath.Join(filesDir, rel)
		info, err := os.Stat(src)
		if os.IsNotExist(err) {
			if err := os.RemoveAll(dst); err != nil {
				return fmt.Errorf("failed to remove stale companion state %s: %w", configuredPath, err)
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := syncDir(src, dst); err != nil {
				return fmt.Errorf("failed to restore companion state %s: %w", configuredPath, err)
			}
		} else if err := copyFileHelper(src, dst); err != nil {
			return fmt.Errorf("failed to restore companion state %s: %w", configuredPath, err)
		}
	}
	return nil
}

func sqliteCompanionRoot(target config.Target) string {
	path := config.ExpandPath(target.Path)
	if filepath.Base(path) == "Cookies" {
		return filepath.Dir(path)
	}
	return path
}

func (s *SQLiteAdapter) validateCookies(dbPath string, required []string) error {
	for _, name := range required {
		out, err := s.sqliteOutput(dbPath, fmt.Sprintf("SELECT COUNT(*) FROM cookies WHERE %s AND name = %s;", cookieHostSQL(), sqliteString(name)))
		if err != nil {
			return fmt.Errorf("failed to validate cookie %s: %w", name, err)
		}
		if strings.TrimSpace(out) == "0" {
			return fmt.Errorf("Claude Desktop cookie profile is missing required cookie %q; sign in to Claude Desktop with the intended account, quit Claude Desktop, then save this profile again", name)
		}
	}
	return nil
}

func (s *SQLiteAdapter) cookieColumns(dbPath string) ([]string, error) {
	out, err := s.sqliteOutput(dbPath, "PRAGMA table_info(cookies);")
	if err != nil {
		return nil, fmt.Errorf("failed to inspect cookie database columns: %w", err)
	}
	var columns []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}
		columns = append(columns, parts[1])
	}
	return columns, nil
}

func (s *SQLiteAdapter) runSQLite(dbPath string, sql string) error {
	_, err := s.sqliteOutput(dbPath, sql)
	return err
}

func (s *SQLiteAdapter) sqliteOutput(dbPath string, sql string) (string, error) {
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

func (s *SQLiteAdapter) removeSQLiteSidecars(dbPath string) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		_ = os.Remove(dbPath + suffix)
	}
	_ = os.Remove(strings.TrimSuffix(dbPath, "Cookies") + "Cookies-journal")
}

func cookieNames(target config.Target) []string {
	if len(target.Keys) > 0 {
		return target.Keys
	}
	return []string{"sessionKey", "sessionKeyLC", "routingHint", "lastActiveOrg", "anthropic-device-id", "cf_clearance", "__cf_bm"}
}

func requiredCookieNames(targetID string) []string {
	if targetID == "claude_desktop" {
		return requiredClaudeDesktopCookies
	}
	return nil
}

func cookieFilterSQL(names []string) string {
	return fmt.Sprintf("%s AND name IN (%s)", cookieHostSQL(), sqliteStringList(names))
}

func cookieHostSQL() string {
	return "(host_key = 'claude.ai' OR host_key = '.claude.ai' OR host_key LIKE '%%.claude.ai')"
}

func sqliteStringList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, sqliteString(value))
	}
	return strings.Join(quoted, ", ")
}

func sqliteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func commonColumns(primary, secondary []string) []string {
	secondarySet := make(map[string]struct{}, len(secondary))
	for _, column := range secondary {
		secondarySet[column] = struct{}{}
	}
	var columns []string
	for _, column := range primary {
		if _, ok := secondarySet[column]; ok {
			columns = append(columns, column)
		}
	}
	return columns
}

func quoteIdentifiers(columns []string) string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, quoteIdentifier(column))
	}
	return strings.Join(quoted, ", ")
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
