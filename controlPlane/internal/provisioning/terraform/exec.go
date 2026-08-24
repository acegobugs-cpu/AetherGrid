package terraform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
)

// commandResult captures the outcome of one external process invocation.
type commandResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// runner abstracts process execution so tests can record and stub Terraform
// commands without invoking a real binary.
type runner interface {
	run(ctx context.Context, dir, bin string, args ...string) (commandResult, error)
}

// osRunner executes commands with the standard library. It never uses a shell:
// arguments are passed verbatim to the binary.
type osRunner struct{}

func (osRunner) run(ctx context.Context, dir, bin string, args ...string) (commandResult, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	// Environment is passed deliberately: only the inherited environment is
	// available to Terraform. Provider credentials are never injected or
	// logged here.
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := commandResult{stdout: stdout.String(), stderr: stderr.String()}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.exitCode = exitErr.ExitCode()
		}
		return result, err
	}
	return result, nil
}
