package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// Module is one member of a Go workspace: the module path it declares in its
// own go.mod, and the directory holding that go.mod.
type Module struct {
	Path string
	Dir  string
}

// FindWorkFile walks up from dir looking for go.work, the way the go command
// does. The working directory only locates the workspace; it never selects
// which modules get tidied.
func FindWorkFile(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	start := dir
	for {
		candidate := filepath.Join(dir, "go.work")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.work found in %s or any parent directory", start)
		}
		dir = parent
	}
}

// ReadWorkspace returns every module in the go.work use block, with the module
// path read from each one's own go.mod rather than guessed from its directory.
func ReadWorkspace(workFile string) ([]Module, error) {
	data, err := os.ReadFile(workFile)
	if err != nil {
		return nil, err
	}
	wf, err := modfile.ParseWork(workFile, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", workFile, err)
	}
	root := filepath.Dir(workFile)

	mods := make([]Module, 0, len(wf.Use))
	for _, use := range wf.Use {
		dir := use.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		path, err := declaredModulePath(dir)
		if err != nil {
			return nil, err
		}
		mods = append(mods, Module{Path: path, Dir: dir})
	}
	return mods, nil
}

func declaredModulePath(dir string) (string, error) {
	p := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	mf, err := modfile.Parse(p, data, nil)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", p, err)
	}
	if mf.Module == nil {
		return "", fmt.Errorf("%s has no module directive", p)
	}
	return mf.Module.Mod.Path, nil
}
