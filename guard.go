package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// backupSuffix is appended to go.mod while a module is mid-transaction. It is
// deliberately visible rather than hidden: if a run dies, the user should see
// the file and understand what happened.
const backupSuffix = ".worktidy-backup"

func backupPath(dir string) string { return filepath.Join(dir, "go.mod"+backupSuffix) }

// Snapshot copies dir/go.mod aside. It refuses if a backup is already there,
// because that means a previous run died and overwriting would destroy the
// original the user still needs.
func Snapshot(dir string) error {
	bak := backupPath(dir)
	if _, err := os.Stat(bak); err == nil {
		return fmt.Errorf("%s already exists: a previous run did not finish. "+
			"Inspect it, restore it over go.mod if it is the one you want, then delete it", bak)
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	return os.WriteFile(bak, data, 0o644)
}

// Restore puts the snapshot back over go.mod and removes it.
func Restore(dir string) error {
	bak := backupPath(dir)
	data, err := os.ReadFile(bak)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), data, 0o644); err != nil {
		return err
	}
	return os.Remove(bak)
}

// Discard keeps the current go.mod and removes the snapshot.
func Discard(dir string) error { return os.Remove(backupPath(dir)) }
