package adapter

import (
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/config"
)

type Adapter interface {
	// Save reads current credentials from the target system and saves them to the profile store.
	Save(target config.Target, targetID string, profileName string) error
	// Load reads credentials from the profile store and writes them to the target system.
	Load(target config.Target, targetID string, profileName string) error
	// IsInstalled checks if the target's credential store or application is installed/configured.
	IsInstalled(target config.Target) bool
}

type ProcessCloser interface {
	CloseProcesses(target config.Target) ([]string, error)
}

type ProcessGuarder interface {
	RunningProcesses(target config.Target) []string
}

// SessionResetter is implemented by adapters that can clear a live local
// session without deleting the target's shared app data. This is useful for
// "sign in to another account" flows where using the app's in-product logout
// would revoke the saved server-side token.
type SessionResetter interface {
	ClearSession(target config.Target, targetID string) error
}

// ActiveChecker is an optional capability for adapters whose profiles are
// referenced by a live system path (e.g. an Electron userData symlink).
// The engine uses it to refuse destructive operations on the active
// profile, which would otherwise leave the live system in a broken state.
type ActiveChecker interface {
	IsActiveProfile(target config.Target, targetID, profileName string) (bool, error)
}

// DeleteOverride lets an adapter opt out of the default "refuse to delete
// the active profile" behavior. Adapters that separate the live state from
// the snapshots (so deleting a snapshot cannot break the live system) should
// implement this and return true to allow deletion.
type DeleteOverride interface {
	CanDeleteProfile(target config.Target, targetID, profileName string) bool
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
	case config.TypeClaudeDesk:
		return &ClaudeDesktopAdapter{}, nil
	case config.TypeElectronUserdata:
		return &ElectronUserdataAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported target type: %s", targetType)
	}
}
