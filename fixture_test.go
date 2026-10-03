package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fixture is a throwaway Go workspace on disk: an app module that imports a
// sibling library whose module path looks remote but was never published.
// That is the exact shape that makes `go mod tidy` fail, measured on Go 1.26.2.
type fixture struct {
	Root string // workspace root, holds go.work
	App  string // app module dir
	Lib  string // lib module dir
}

// unpublishedLib is a module path that must never resolve. The random-looking
// owner segment is deliberate: if this ever became a real repository the tests
// would start passing for the wrong reason.
const unpublishedLib = "github.com/worktidy-test-unpublished-xyz/lib"

// newFixture writes the workspace into t.TempDir and returns its paths.
func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{
		Root: root,
		App:  filepath.Join(root, "app"),
		Lib:  filepath.Join(root, "lib"),
	}
	mustMkdir(t, f.App)
	mustMkdir(t, f.Lib)

	write(t, filepath.Join(f.Lib, "go.mod"), "module "+unpublishedLib+"\n\ngo 1.22\n")
	write(t, filepath.Join(f.Lib, "lib.go"), "package lib\n\nfunc Hello() string { return \"hi\" }\n")

	write(t, filepath.Join(f.App, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	write(t, filepath.Join(f.App, "main.go"),
		"package main\n\nimport (\n\t\"fmt\"\n\n\t\""+unpublishedLib+"\"\n)\n\nfunc main() { fmt.Println(lib.Hello()) }\n")

	write(t, filepath.Join(root, "go.work"), "go 1.22\n\nuse (\n\t./app\n\t./lib\n)\n")
	return f
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestFixtureReproducesTheBug(t *testing.T) {
	f := newFixture(t)

	// The workspace itself must work: build and run succeed.
	if out, err := runGo(t, f.App, nil, "build", "."); err != nil {
		t.Fatalf("go build in workspace must succeed, got %v\n%s", err, out)
	}

	// And `go mod tidy` must fail, because it ignores go.work and goes to the
	// network for a module that was never published.
	out, err := runGo(t, f.App, nil, "mod", "tidy")
	if err == nil {
		t.Fatalf("go mod tidy unexpectedly succeeded; the fixture no longer reproduces golang/go#50750:\n%s", out)
	}
	t.Logf("go mod tidy failed as expected:\n%s", out)
}

// runGo runs the go command in dir with extra environment entries appended.
func runGo(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
