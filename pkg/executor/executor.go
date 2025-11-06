// Package executor provides a small helper to run external processes (like the leo CLI)
package executor

import (
	"context"
	"errors"
	"leo-cli-lambda/pkg/utils"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type Config struct {
	BinPath        string
	Args           []string
	WorkDir        string
	MaxOutputBytes int
}

type Result struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	Truncated bool
}

const defaultMaxOutputBytes = 64 * 1024

var (
	stdOutExcludedStrings = []string{"Installation"}
	stdErrExcludedStrings = []string{"Failed to store", "powers-of-beta"}
)

// Run executes the provided command with the given configuration.
func Run(ctx context.Context, cfg Config) Result {
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = defaultMaxOutputBytes
	}

	cmd := exec.CommandContext(ctx, cfg.BinPath, cfg.Args...)
	cmd.Dir = cfg.WorkDir

	if cfg.WorkDir != "" {
		if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
			errMsg, truncated := clipToLimit(err.Error(), cfg.MaxOutputBytes)
			return Result{
				ExitCode:  1,
				Stderr:    strings.TrimSpace(errMsg),
				Truncated: truncated,
			}
		}
	}

	stdoutBuf := newLimitedBuffer(cfg.MaxOutputBytes)
	stderrBuf := newLimitedBuffer(cfg.MaxOutputBytes)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	runErr := cmd.Run()

	res := Result{
		Stdout:    utils.FilterLines(stdoutBuf.String(), stdOutExcludedStrings),
		Stderr:    utils.FilterLines(stderrBuf.String(), stdErrExcludedStrings),
		Truncated: stdoutBuf.Truncated || stderrBuf.Truncated,
	}

	if runErr == nil {
		res.Stderr = strings.TrimSpace(res.Stderr)
		return res
	}

	res.ExitCode = exitCodeFromError(runErr)
	combined, errTruncated := appendError(res.Stderr, runErr, cfg.MaxOutputBytes)
	res.Stderr = strings.TrimSpace(combined)
	res.Truncated = res.Truncated || errTruncated
	if res.Stderr == "" {
		res.Stderr = runErr.Error()
	}
	return res
}

func exitCodeFromError(runErr error) int {
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		if ee.ProcessState != nil {
			if status, ok := ee.Sys().(syscall.WaitStatus); ok {
				return status.ExitStatus()
			}
			return ee.ExitCode()
		}
	}
	return 1
}

func appendError(stderr string, runErr error, limit int) (string, bool) {
	msg := runErr.Error()
	if stderr == "" {
		return clipToLimit(msg, limit)
	}
	if strings.Contains(stderr, msg) {
		return stderr, false
	}
	combined := stderr + "\n" + msg
	return clipToLimit(combined, limit)
}

func clipToLimit(val string, limit int) (string, bool) {
	if limit <= 0 || len(val) <= limit {
		return val, false
	}
	return val[len(val)-limit:], true
}

type limitedBuffer struct {
	buf       []byte
	Limit     int
	Truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{Limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.Limit <= 0 {
		b.buf = append(b.buf, p...)
		return len(p), nil
	}
	if len(p) >= b.Limit {
		if len(b.buf) > 0 {
			b.Truncated = true
		}
		b.buf = append(b.buf[:0], p[len(p)-b.Limit:]...)
		b.Truncated = true
		return len(p), nil
	}
	free := b.Limit - len(b.buf)
	if len(p) <= free {
		b.buf = append(b.buf, p...)
		return len(p), nil
	}
	drop := min(len(p)-free, len(b.buf))
	b.buf = append(b.buf[drop:], p...)
	b.Truncated = true
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.buf)
}
