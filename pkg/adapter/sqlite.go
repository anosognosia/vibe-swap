package adapter

import (
	"errors"
	"os"
	"vibeswap/pkg/config"
)

type SQLiteAdapter struct{}

func (s *SQLiteAdapter) Save(target config.Target, targetID string, profileName string) error {
	return errors.New("SQLite database switching is deferred for future implementation")
}

func (s *SQLiteAdapter) Load(target config.Target, targetID string, profileName string) error {
	return errors.New("SQLite database switching is deferred for future implementation")
}

func (s *SQLiteAdapter) IsInstalled(target config.Target) bool {
	// For future verification, check if SQLite file exists
	path := config.ExpandPath(target.Path)
	_, err := os.Stat(path)
	return err == nil
}
