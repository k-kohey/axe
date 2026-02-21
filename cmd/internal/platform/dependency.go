package platform

import (
	"fmt"
	"os/exec"
)

// LookPather abstracts exec.LookPath for testing.
type LookPather interface {
	LookPath(file string) (string, error)
}

type defaultLookPather struct{}

func (defaultLookPather) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// DefaultLookPather returns the standard LookPather using exec.LookPath.
func DefaultLookPather() LookPather {
	return defaultLookPather{}
}

// CheckIDBCompanion validates that idb_companion is available in PATH.
func CheckIDBCompanion() error {
	return CheckIDBCompanionWith(DefaultLookPather())
}

// CheckIDBCompanionWith validates idb_companion using the given LookPather.
func CheckIDBCompanionWith(lp LookPather) error {
	_, err := lp.LookPath("idb_companion")
	if err != nil {
		return fmt.Errorf("idb_companion not found in PATH. Install via: brew install facebook/fb/idb-companion")
	}
	return nil
}
