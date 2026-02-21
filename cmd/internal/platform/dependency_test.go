package platform

import (
	"errors"
	"strings"
	"testing"
)

type fakeLookPather struct {
	found map[string]string // file → path
}

func (f fakeLookPather) LookPath(file string) (string, error) {
	if p, ok := f.found[file]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func TestCheckIDBCompanion_Found(t *testing.T) {
	lp := fakeLookPather{found: map[string]string{
		"idb_companion": "/usr/local/bin/idb_companion",
	}}
	if err := CheckIDBCompanionWith(lp); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckIDBCompanion_NotFound(t *testing.T) {
	lp := fakeLookPather{found: map[string]string{}}
	err := CheckIDBCompanionWith(lp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "idb_companion not found") {
		t.Errorf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), "brew") {
		t.Errorf("error should mention brew install: %v", err)
	}
}
