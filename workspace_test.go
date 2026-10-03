package main

import (
	"path/filepath"
	"testing"
)

func TestFindWorkFileWalksUp(t *testing.T) {
	f := newFixture(t)

	// Called from inside a member module, it must find the workspace root.
	got, err := FindWorkFile(f.App)
	if err != nil {
		t.Fatalf("FindWorkFile: %v", err)
	}
	want := filepath.Join(f.Root, "go.work")
	if got != want {
		t.Fatalf("FindWorkFile = %q, want %q", got, want)
	}
}

func TestFindWorkFileFailsOutsideWorkspace(t *testing.T) {
	if _, err := FindWorkFile(t.TempDir()); err == nil {
		t.Fatal("FindWorkFile must fail when there is no go.work anywhere above")
	}
}

func TestReadWorkspaceResolvesModulePaths(t *testing.T) {
	f := newFixture(t)
	mods, err := ReadWorkspace(filepath.Join(f.Root, "go.work"))
	if err != nil {
		t.Fatalf("ReadWorkspace: %v", err)
	}
	if len(mods) != 2 {
		t.Fatalf("got %d modules, want 2: %+v", len(mods), mods)
	}

	byPath := map[string]string{}
	for _, m := range mods {
		byPath[m.Path] = m.Dir
	}
	if byPath["example.com/app"] != f.App {
		t.Errorf("app dir = %q, want %q", byPath["example.com/app"], f.App)
	}
	if byPath[unpublishedLib] != f.Lib {
		t.Errorf("lib dir = %q, want %q", byPath[unpublishedLib], f.Lib)
	}
}
