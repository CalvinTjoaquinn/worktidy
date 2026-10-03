package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAddReplacesReportsWhatItAdded(t *testing.T) {
	f := newFixture(t)
	mods, err := ReadWorkspace(filepath.Join(f.Root, "go.work"))
	if err != nil {
		t.Fatal(err)
	}

	added, err := AddReplaces(f.App, mods)
	if err != nil {
		t.Fatalf("AddReplaces: %v", err)
	}

	// app itself is in mods and must never be replaced into itself.
	for _, p := range added {
		if p == "example.com/app" {
			t.Fatal("AddReplaces replaced the target module with itself")
		}
	}
	if len(added) != 1 || added[0] != unpublishedLib {
		t.Fatalf("added = %v, want exactly [%s]", added, unpublishedLib)
	}
	if got := read(t, filepath.Join(f.App, "go.mod")); !strings.Contains(got, "replace "+unpublishedLib) {
		t.Fatalf("go.mod has no replace for the sibling:\n%s", got)
	}
}

// CONTROL: a replace the user wrote must be invisible to AddReplaces and must
// survive DropReplaces. This is the half that proves the tool does not damage.
func TestUserReplaceIsNeverTouched(t *testing.T) {
	f := newFixture(t)
	appMod := filepath.Join(f.App, "go.mod")
	write(t, appMod, "module example.com/app\n\ngo 1.22\n\nreplace example.com/other => ./vendored-other\n")

	mods, err := ReadWorkspace(filepath.Join(f.Root, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	added, err := AddReplaces(f.App, mods)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range added {
		if p == "example.com/other" {
			t.Fatal("AddReplaces claimed the user's replace as its own")
		}
	}
	if err := DropReplaces(f.App, added); err != nil {
		t.Fatal(err)
	}
	got := read(t, appMod)
	if !strings.Contains(got, "example.com/other => ./vendored-other") {
		t.Fatalf("the user's replace was removed:\n%s", got)
	}
	if strings.Contains(got, unpublishedLib+" =>") {
		t.Fatalf("our own replace survived DropReplaces:\n%s", got)
	}
}

// CONTROL: when the user already replaced a sibling, we must not duplicate it
// and must not remove it.
func TestExistingSiblingReplaceIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	appMod := filepath.Join(f.App, "go.mod")
	write(t, appMod, "module example.com/app\n\ngo 1.22\n\nreplace "+unpublishedLib+" => ../lib\n")

	mods, err := ReadWorkspace(filepath.Join(f.Root, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	added, err := AddReplaces(f.App, mods)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v, want empty: the sibling was already replaced by the user", added)
	}
	if err := DropReplaces(f.App, added); err != nil {
		t.Fatal(err)
	}
	if got := read(t, appMod); !strings.Contains(got, unpublishedLib+" => ../lib") {
		t.Fatalf("the user's sibling replace was removed:\n%s", got)
	}
}
