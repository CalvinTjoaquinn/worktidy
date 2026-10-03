package main

import (
	"os"
	"os/exec"
)

// RunTidy runs `go mod tidy` in dir with GOWORK=off. The GOWORK=off is the
// whole trick: in workspace mode tidy refuses to run, and in module mode the
// temporary replace directives we just wrote are what let it resolve siblings
// from disk instead of the network.
func RunTidy(dir string) (string, error) {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
