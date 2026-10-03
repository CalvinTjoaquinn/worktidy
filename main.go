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
	"sync"
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

	// inFlight is the module currently mid-transaction. The signal handler
	// restores it so Ctrl-C cannot leave a replace directive behind.
	var mu sync.Mutex
	inFlight := ""
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		mu.Lock()
		if inFlight != "" {
			_ = Restore(inFlight)
		}
		mu.Unlock()
		fmt.Fprintln(os.Stderr, "\nworktidy: interrupted, go.mod restored")
		os.Exit(exitSignalled)
	}()

	for _, m := range mods {
		before, err := os.ReadFile(filepath.Join(m.Dir, "go.mod"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "worktidy:", err)
			return exitSetup
		}
		if err := Snapshot(m.Dir); err != nil {
			fmt.Fprintln(os.Stderr, "worktidy:", err)
			return exitSetup
		}
		mu.Lock()
		inFlight = m.Dir
		mu.Unlock()

		added, addErr := AddReplaces(m.Dir, mods)
		var out string
		if addErr == nil {
			out, err = RunTidy(m.Dir)
		} else {
			err = addErr
		}

		if err != nil {
			_ = Restore(m.Dir)
			mu.Lock()
			inFlight = ""
			mu.Unlock()
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
			mu.Lock()
			inFlight = ""
			mu.Unlock()
			fmt.Fprintln(os.Stderr, "worktidy:", err)
			return exitTidyFail
		}
		if err := Discard(m.Dir); err != nil {
			fmt.Fprintln(os.Stderr, "worktidy:", err)
			return exitTidyFail
		}
		mu.Lock()
		inFlight = ""
		mu.Unlock()

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
