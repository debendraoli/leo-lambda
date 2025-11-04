package executor

import (
	"context"
	"strings"
	"testing"
)

func TestRunEcho(t *testing.T) {
	bin := "echo"

	res := Run(context.Background(), Config{
		BinPath: bin,
		Args:    []string{"hello", "world"},
	})
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := res.Stdout; got == "" {
		t.Fatalf("expected some stdout, got empty")
	}
	if res.Truncated {
		t.Fatalf("unexpected truncation for echo command")
	}
}

func TestRun_TruncatesAndKeepsTail(t *testing.T) {
	cmd := "for i in $(seq 1 200); do echo line-$i; done"
	res := Run(context.Background(), Config{
		BinPath:        "/bin/sh",
		Args:           []string{"-c", cmd},
		MaxOutputBytes: 512,
	})
	if !res.Truncated {
		t.Fatalf("expected truncation for large output")
	}
	if strings.Contains(res.Stdout, "line-1\n") {
		t.Fatalf("expected oldest lines to be dropped, found line-1 in %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "line-200") {
		t.Fatalf("expected tail of output to be preserved, got %q", res.Stdout)
	}
}
