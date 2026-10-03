package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// existingReplaces is the set of module paths dir's go.mod already replaces.
// Those belong to the user and are never touched.
func existingReplaces(dir string) (map[string]bool, error) {
	p := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	mf, err := modfile.Parse(p, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	set := make(map[string]bool, len(mf.Replace))
	for _, r := range mf.Replace {
		set[r.Old.Path] = true
	}
	return set, nil
}

// AddReplaces points each workspace sibling at its directory on disk and
// returns the module paths it actually added. The target module itself and any
// sibling the user already replaced are skipped, so the returned slice is
// exactly what DropReplaces must remove.
//
// Mutation goes through `go mod edit` rather than modfile.Format because
// Format canonicalises the whole file: rewriting an unchanged go.mod could
// still change its bytes, and "an untouched module is byte-identical
// afterwards" is half of this tool's correctness.
func AddReplaces(dir string, mods []Module) ([]string, error) {
	self, err := declaredModulePath(dir)
	if err != nil {
		return nil, err
	}
	already, err := existingReplaces(dir)
	if err != nil {
		return nil, err
	}

	var added []string
	for _, m := range mods {
		if m.Path == self || already[m.Path] {
			continue
		}
		rel, err := filepath.Rel(dir, m.Dir)
		if err != nil {
			return added, err
		}
		if err := goModEdit(dir, "-replace", m.Path+"="+rel); err != nil {
			return added, err
		}
		added = append(added, m.Path)
	}
	return added, nil
}

// DropReplaces removes the replace directives for exactly these module paths.
func DropReplaces(dir string, paths []string) error {
	for _, p := range paths {
		if err := goModEdit(dir, "-dropreplace", p); err != nil {
			return err
		}
	}
	return nil
}

func goModEdit(dir string, args ...string) error {
	cmd := exec.Command("go", append([]string{"mod", "edit"}, args...)...)
	cmd.Dir = dir
	// GOWORK=off so `go mod edit` acts on this module's go.mod and not on the
	// workspace.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod edit %v in %s: %w\n%s", args, dir, err, out)
	}
	return nil
}
