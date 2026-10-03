package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// buildWorktidy compiles the command once per test binary run.
func buildWorktidy(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "worktidy")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("building worktidy: %v\n%s", err, out)
	}
	return bin
}

func runWorktidy(t *testing.T, bin, dir string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Dir = dir
	// The binary shells out to `go mod tidy`, which inherits this environment,
	// so the hermetic settings have to be passed through explicitly.
	cmd.Env = append(os.Environ(), hermeticEnv()...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("running worktidy: %v\n%s", err, out)
	}
	return string(out), ee.ExitCode()
}

func TestWorktidyTidiesAndLeavesNoReplace(t *testing.T) {
	bin := buildWorktidy(t)
	f := newFixture(t)

	out, code := runWorktidy(t, bin, f.App)
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	got := read(t, filepath.Join(f.App, "go.mod"))
	if !strings.Contains(got, "require "+unpublishedLib) {
		t.Errorf("go.mod has no require for the sibling:\n%s", got)
	}
	if strings.Contains(got, "replace") {
		t.Errorf("go.mod still has a replace directive:\n%s", got)
	}
}

func TestBuildStillWorksAfterWorktidy(t *testing.T) {
	bin := buildWorktidy(t)
	f := newFixture(t)
	if _, code := runWorktidy(t, bin, f.App); code != 0 {
		t.Fatalf("worktidy exit %d", code)
	}
	if out, err := runGo(t, f.App, nil, "build", "."); err != nil {
		t.Fatalf("go build broke after worktidy: %v\n%s", err, out)
	}
}

// CONTROL: a module needing nothing must come out byte-identical.
func TestCleanModuleIsByteIdentical(t *testing.T) {
	bin := buildWorktidy(t)
	f := newFixture(t)
	libMod := filepath.Join(f.Lib, "go.mod")
	before := read(t, libMod)

	if _, code := runWorktidy(t, bin, f.App); code != 0 {
		t.Fatal("worktidy failed")
	}
	if after := read(t, libMod); after != before {
		t.Fatalf("lib/go.mod changed although lib needs nothing:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestIdempotent(t *testing.T) {
	bin := buildWorktidy(t)
	f := newFixture(t)
	appMod := filepath.Join(f.App, "go.mod")

	if _, code := runWorktidy(t, bin, f.App); code != 0 {
		t.Fatal("first run failed")
	}
	first := read(t, appMod)
	if _, code := runWorktidy(t, bin, f.App); code != 0 {
		t.Fatal("second run failed")
	}
	if second := read(t, appMod); second != first {
		t.Fatalf("not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestNoBackupLeftBehind(t *testing.T) {
	bin := buildWorktidy(t)
	f := newFixture(t)
	if _, code := runWorktidy(t, bin, f.App); code != 0 {
		t.Fatal("worktidy failed")
	}
	var found []string
	_ = filepath.Walk(f.Root, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, backupSuffix) {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 0 {
		t.Fatalf("backup files left behind: %v", found)
	}
}

// CONTROL: a genuinely broken import must leave go.mod exactly as it was.
func TestRestoresOnTidyFailure(t *testing.T) {
	bin := buildWorktidy(t)
	f := newFixture(t)
	appMod := filepath.Join(f.App, "go.mod")
	write(t, filepath.Join(f.App, "broken.go"),
		"package main\n\nimport _ \"github.com/worktidy-also-missing-xyz/nope\"\n")
	before := read(t, appMod)

	out, code := runWorktidy(t, bin, f.App)
	if code == 0 {
		t.Fatalf("worktidy succeeded despite an unresolvable import:\n%s", out)
	}
	if after := read(t, appMod); after != before {
		t.Fatalf("go.mod was not restored:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Stat(backupPath(f.App)); err == nil {
		t.Error("backup left behind after a failure")
	}
}

func TestRefusesWhenBackupExists(t *testing.T) {
	bin := buildWorktidy(t)
	f := newFixture(t)
	appMod := filepath.Join(f.App, "go.mod")
	before := read(t, appMod)
	write(t, backupPath(f.App), "module example.com/app\n\ngo 1.22\n")

	out, code := runWorktidy(t, bin, f.App)
	if code == 0 {
		t.Fatalf("worktidy ran although a backup existed:\n%s", out)
	}
	if after := read(t, appMod); after != before {
		t.Fatal("go.mod was modified although the run was refused")
	}
}

func TestFailsOutsideWorkspace(t *testing.T) {
	bin := buildWorktidy(t)
	out, code := runWorktidy(t, bin, t.TempDir())
	if code != 1 {
		t.Fatalf("exit %d, want 1 outside a workspace:\n%s", code, out)
	}
	if !strings.Contains(out, "go.work") {
		t.Errorf("the error should name go.work:\n%s", out)
	}
}

// wideFixture is a workspace with several sibling modules, so that tidying the
// app takes long enough to be interrupted partway through.
func wideFixture(t *testing.T, siblings int) string {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	mustMkdir(t, app)

	uses := "\t./app\n"
	imports := ""
	for k := 1; k <= siblings; k++ {
		name := fmt.Sprintf("lib%d", k)
		dir := filepath.Join(root, name)
		mustMkdir(t, dir)
		path := fmt.Sprintf("github.com/worktidy-test-unpublished-xyz/%s", name)
		write(t, filepath.Join(dir, "go.mod"), "module "+path+"\n\ngo 1.22\n")
		write(t, filepath.Join(dir, name+".go"), "package "+name+"\n\nfunc Hello() string { return \"hi\" }\n")
		uses += "\t./" + name + "\n"
		imports += "\t_ \"" + path + "\"\n"
	}
	write(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	write(t, filepath.Join(app, "main.go"), "package main\n\nimport (\n"+imports+")\n\nfunc main() {}\n")
	write(t, filepath.Join(root, "go.work"), "go 1.22\n\nuse (\n"+uses+")\n")
	return root
}

// TestInterruptLeavesNoMess asserts invariants rather than a point in time,
// because where a signal lands is not controllable. Whenever it lands, two
// things must hold: no backup file survives anywhere in the workspace, and no
// go.mod keeps a replace directive. The first version of this program violated
// both — it deleted the backup, left a replace behind, and printed "go.mod
// restored" while doing it.
func TestInterruptLeavesNoMess(t *testing.T) {
	bin := buildWorktidy(t)

	const iterations = 8
	midTransaction := 0
	for i := 0; i < iterations; i++ {
		root := wideFixture(t, 12)
		app := filepath.Join(root, "app")

		cmd := exec.Command(bin)
		cmd.Dir = app
		cmd.Env = append(os.Environ(), hermeticEnv()...)
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting worktidy: %v", err)
		}

		// Wait for a transaction to actually be open, rather than sleeping a
		// guessed amount. Measured on Go 1.26.2: the first backup appears about
		// 5ms in while a full 13-module run takes about 3.4s, so any fixed short
		// sleep lands before the first Snapshot — before signal.Notify is even
		// installed, where SIGINT just kills the process by default disposition
		// and nothing is proved.
		bak := filepath.Join(app, "go.mod"+backupSuffix)
		sawBackup := false
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(bak); err == nil {
				sawBackup = true
				break
			}
			time.Sleep(100 * time.Microsecond)
		}
		// Jitter so successive iterations land at different points inside the
		// transaction: early is still in AddReplaces' run of `go mod edit`
		// calls, later is in tidy or past DropReplaces.
		time.Sleep(time.Duration(i) * 2 * time.Millisecond)

		_ = cmd.Process.Signal(syscall.SIGINT)
		_ = cmd.Wait()
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		if sawBackup && code == exitSignalled {
			midTransaction++
		}

		// Settle before asserting. A `go mod edit` child is a separate process:
		// if the parent exits while one is mid-write, that write still lands,
		// and a check racing ahead of it would miss exactly the damage this
		// test exists to catch.
		time.Sleep(50 * time.Millisecond)

		// Exit code is deliberately not asserted: 0 means it finished before
		// the signal arrived, 130 means it stopped. Either is legitimate.
		var backups, withReplace []string
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(p, backupSuffix) {
				backups = append(backups, p)
			}
			if filepath.Base(p) == "go.mod" {
				b, rerr := os.ReadFile(p)
				if rerr == nil && strings.Contains(string(b), "replace") {
					withReplace = append(withReplace, p+":\n"+string(b))
				}
			}
			return nil
		})
		if len(backups) != 0 {
			t.Errorf("iteration %d (exit %d): backup files left behind: %v", i, code, backups)
		}
		if len(withReplace) != 0 {
			t.Errorf("iteration %d (exit %d): replace directive left behind:\n%s", i, code, strings.Join(withReplace, "\n"))
		}
	}
	t.Logf("%d of %d iterations were interrupted with a transaction open (exit %d); the rest finished first",
		midTransaction, iterations, exitSignalled)
	if midTransaction == 0 {
		t.Errorf("no iteration was interrupted with a transaction open, so this test proved nothing; "+
			"all %d runs finished before the signal landed", iterations)
	}
}

// TestInterruptedReportsHonestly pins both messages, because the first version
// of this program printed "go.mod restored" even when it had restored nothing.
// The no-op branch is reached when a signal lands between modules, a window too
// narrow to hit reliably by timing, so it is checked directly here.
func TestInterruptedReportsHonestly(t *testing.T) {
	cases := []struct {
		restored string
		want     string
		notWant  string
	}{
		{"", "nothing was modified", "restored"},
		{filepath.FromSlash("/tmp/x"), filepath.FromSlash("/tmp/x") + "/go.mod restored", "nothing was modified"},
	}
	for _, c := range cases {
		got, code := captureStderr(t, func() int { return interrupted(c.restored) })
		if code != exitSignalled {
			t.Errorf("interrupted(%q) returned %d, want %d", c.restored, code, exitSignalled)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("interrupted(%q) printed %q, want it to contain %q", c.restored, got, c.want)
		}
		if strings.Contains(got, c.notWant) {
			t.Errorf("interrupted(%q) printed %q, which must not contain %q", c.restored, got, c.notWant)
		}
	}
}

func captureStderr(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	code := fn()
	os.Stderr = orig
	_ = w.Close()
	b, _ := io.ReadAll(r)
	_ = r.Close()
	return string(b), code
}
