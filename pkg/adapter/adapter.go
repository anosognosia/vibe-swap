package adapter

import (
	"fmt"
	"vibeswap/pkg/config"
)

type Adapter interface {
	// Save reads current credentials from the target system and saves them to the profile store.
	Save(target config.Target, targetID string, profileName string) error
	// Load reads credentials from the profile store and writes them to the target system.
	Load(target config.Target, targetID string, profileName string) error
	// IsInstalled checks if the target's credential store or application is installed/configured.
	IsInstalled(target config.Target) bool
}

func GetAdapter(targetType config.TargetType) (Adapter, error) {
	switch targetType {
	case config.TypeFile:
		return &FileAdapter{}, nil
	case config.TypeJSONKey:
		return &JSONKeyAdapter{}, nil
	case config.TypeKeychain:
		return &KeychainAdapter{}, nil
	case config.TypeSQLite:
		return &SQLiteAdapter{}, nil
	case config.TypeWrappedDir:
		return &WrappedDirAdapter{}, nil
	case config.TypeElectron:
		return &ElectronAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported target type: %s", targetType)
	}
}
