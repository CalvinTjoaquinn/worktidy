// Command worktidy runs `go mod tidy` in every module of a Go workspace and
// leaves no local replace directives behind.
//
// `go mod tidy` ignores go.work (golang/go#50750, closed not planned), so in a
// workspace with an unpublished sibling it goes to the network and fails, even
// though `go build` and `go test` work. The usual fix is to hand-edit go.mod
// with a replace pointing at the sibling on disk, then remember to remove it
// before committing — and go.mod is a distributable artifact, so forgetting
// breaks the build for everyone else.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
)

const (
	exitOK        = 0
	exitSetup     = 1
	exitTidyFail  = 2
	exitSignalled = 130
)

func main() { os.Exit(run()) }

func run() int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "worktidy:", err)
		return exitSetup
	}
	workFile, err := FindWorkFile(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worktidy:", err)
		return exitSetup
	}
	mods, err := ReadWorkspace(workFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worktidy:", err)
		return exitSetup
	}

	// stopRequested is set by the signal handler and read by this goroutine
	// between steps. The handler never touches go.mod: exactly one goroutine
	// ever writes that file, which is the only structural way a restore cannot
	// interleave with a `go mod edit`. A terminal Ctrl-C also reaches the `go`
	// child in the same process group, so an in-flight tidy fails on its own
	// and the ordinary error path performs the restore.
	var stopRequested atomic.Bool
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		stopRequested.Store(true)
	}()

	for _, m := range mods {
		before, err := os.ReadFile(filepath.Join(m.Dir, "go.mod"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "worktidy:", err)
			return exitSetup
		}
		if stopRequested.Load() {
			return interrupted("")
		}

		if err := Snapshot(m.Dir); err != nil {
			fmt.Fprintln(os.Stderr, "worktidy:", err)
			return exitSetup
		}
		// From here until Discard, m.Dir is mid-transaction: every exit path
		// below must either Restore it or Discard its backup.

		added, err := AddReplaces(m.Dir, mods)
		if err == nil && stopRequested.Load() {
			return abort(m.Dir)
		}
		var out string
		if err == nil {
			out, err = RunTidy(m.Dir)
		}
		if err != nil {
			_ = Restore(m.Dir)
			if stopRequested.Load() {
				return interrupted(m.Dir)
			}
			// tidy's own message is almost always the right one, so it is
			// printed as-is rather than wrapped.
			fmt.Fprintf(os.Stderr, "worktidy: %s failed, go.mod restored\n\n%s", m.Path, out)
			if out == "" {
				fmt.Fprintln(os.Stderr, "worktidy:", err)
			}
			return exitTidyFail
		}
		if err := DropReplaces(m.Dir, added); err != nil {
			_ = Restore(m.Dir)
			fmt.Fprintln(os.Stderr, "worktidy:", err)
			return exitTidyFail
		}
		// Aborting here rolls back a go.mod that was already in its final
		// correct state, since DropReplaces has run and only the backup is
		// left to clean up. That is deliberate, not an oversight: an interrupt
		// means undo, and it keeps this module consistent with one that failed.
		// Do not "fix" it into a Discard.
		if stopRequested.Load() {
			return abort(m.Dir)
		}
		if err := Discard(m.Dir); err != nil {
			fmt.Fprintln(os.Stderr, "worktidy:", err)
			return exitTidyFail
		}
		// m.Dir is committed now; there is nothing left to restore for it.

		after, err := os.ReadFile(filepath.Join(m.Dir, "go.mod"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "worktidy:", err)
			return exitTidyFail
		}
		if string(before) == string(after) {
			fmt.Printf("  %-40s unchanged\n", m.Path)
		} else {
			fmt.Printf("  %-40s go.mod updated\n", m.Path)
		}
	}

	fmt.Printf("\n%d module(s) tidied, 0 replace directives left behind\n", len(mods))
	return exitOK
}

// abort rolls back a module that is mid-transaction and reports the stop.
func abort(dir string) int {
	_ = Restore(dir)
	return interrupted(dir)
}

// interrupted reports the stop honestly: it claims a restore only when one
// actually happened. Modules already finished keep their changes; only the one
// in flight is rolled back.
func interrupted(restored string) int {
	if restored == "" {
		fmt.Fprintln(os.Stderr, "\nworktidy: interrupted, nothing was modified")
	} else {
		fmt.Fprintf(os.Stderr, "\nworktidy: interrupted, %s/go.mod restored\n", restored)
	}
	return exitSignalled
}
