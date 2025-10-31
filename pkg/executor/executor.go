package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	BinPath        string
	Args           []string
	WorkDir        string
	Timeout        time.Duration
	MaxOutputBytes int
	ExtraEnv       map[string]string
}

type Result struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	Truncated bool
	TimedOut  bool
}

// Run executes the provided command with the given configuration.
func Run(parent context.Context, cfg Config) Result {
	// Defaults
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 5_000_000
	}
	if strings.TrimSpace(cfg.WorkDir) == "" {
		cfg.WorkDir = "."
	}

	// Only enforce a timeout if explicitly provided. Otherwise, rely on the parent
	// context (e.g., Lambda's own timeout) to cancel execution.
	ctx := parent
	var cancel context.CancelFunc = func() {}
	if cfg.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, cfg.Timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(ctx, cfg.BinPath, cfg.Args...)
	cmd.Dir = cfg.WorkDir

	// Always run in a new process group so we can kill the entire subtree on timeout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Merge environment
	env := os.Environ()
	for k, v := range cfg.ExtraEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	var outBuf, errBuf limitedBuffer
	outBuf.Limit = cfg.MaxOutputBytes
	errBuf.Limit = cfg.MaxOutputBytes
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	// Start and wait with context handling so we can kill the process group on timeout
	runErr := cmd.Start()
	if runErr == nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-ctx.Done():
			// Timeout/cancel: kill the entire process group: negative pid targets the pgid
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done // ensure Wait() returns
			runErr = ctx.Err()
		case err := <-done:
			runErr = err
		}
	}

	res := Result{
		Stdout:    outBuf.String(),
		Stderr:    errBuf.String(),
		Truncated: outBuf.Truncated || errBuf.Truncated,
	}

	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	}

	if runErr != nil {
		// Extract exit code when possible
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			// On Unix, get the status
			if status, ok := ee.Sys().(syscall.WaitStatus); ok {
				res.ExitCode = status.ExitStatus()
			} else {
				res.ExitCode = 1
			}
		} else if res.TimedOut {
			res.ExitCode = 124 // conventional timeout code
		} else {
			res.ExitCode = 1
		}
	} else {
		res.ExitCode = 0
	}

	return res
}

// limitedBuffer is a bytes.Buffer-like writer that stops after Limit bytes.
type limitedBuffer struct {
	bytes.Buffer
	Limit     int
	Truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Limit <= 0 {
		return b.Buffer.Write(p)
	}
	remaining := b.Limit - b.Len()
	if remaining <= 0 {
		b.Truncated = true
		return len(p), nil
	}
	if len(p) <= remaining {
		return b.Buffer.Write(p)
	}
	// Write only what fits
	_, _ = b.Buffer.Write(p[:remaining])
	b.Truncated = true
	return len(p), nil
}
