package executor

import (
	"context"
	"testing"
	"time"
)

func TestRunEcho(t *testing.T) {
	bin := "echo"

	res := Run(context.Background(), Config{
		BinPath: bin,
		Args:    []string{"hello", "world"},
		Timeout: 5 * time.Second,
	})
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if res.TimedOut {
		t.Fatalf("unexpected timeout")
	}
	if got := res.Stdout; got == "" {
		t.Fatalf("expected some stdout, got empty")
	}
}

func TestTimeout(t *testing.T) {
	// Use a portable sleep via shell
	bin := "/bin/sh"
	args := []string{"-c", "sleep 2"}
	res := Run(context.Background(), Config{BinPath: bin, Args: args, Timeout: 200 * time.Millisecond})
	if !res.TimedOut {
		t.Fatalf("expected timeout, got %+v", res)
	}
}
