package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	f := newFixture(t)
	appMod := filepath.Join(f.App, "go.mod")
	before := read(t, appMod)

	if err := Snapshot(f.App); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	write(t, appMod, "module example.com/app\n\ngo 1.22\n\n// vandalised\n")
	if err := Restore(f.App); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := read(t, appMod); got != before {
		t.Fatalf("Restore did not return the original bytes:\ngot:\n%s\nwant:\n%s", got, before)
	}
	if _, err := os.Stat(backupPath(f.App)); err == nil {
		t.Fatal("Restore left the backup file behind")
	}
}

func TestDiscardKeepsCurrentAndRemovesBackup(t *testing.T) {
	f := newFixture(t)
	appMod := filepath.Join(f.App, "go.mod")

	if err := Snapshot(f.App); err != nil {
		t.Fatal(err)
	}
	write(t, appMod, "module example.com/app\n\ngo 1.22\n\n// kept\n")
	if err := Discard(f.App); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if got := read(t, appMod); !strings.Contains(got, "// kept") {
		t.Fatalf("Discard reverted the file:\n%s", got)
	}
	if _, err := os.Stat(backupPath(f.App)); err == nil {
		t.Fatal("Discard left the backup file behind")
	}
}

// A backup already on disk means a previous run died mid-transaction.
// Overwriting it would destroy the user's original go.mod, so refuse.
func TestSnapshotRefusesWhenBackupExists(t *testing.T) {
	f := newFixture(t)
	write(t, backupPath(f.App), "module example.com/app\n\ngo 1.22\n")

	if err := Snapshot(f.App); err == nil {
		t.Fatal("Snapshot must refuse when a backup already exists")
	}
}
