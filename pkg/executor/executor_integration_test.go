package executor

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// findLeo returns the path to the leo binary or empty if not found
func findLeo() string {
	if p := os.Getenv("LEO_BIN"); p != "" {
		return p
	}
	if p, err := exec.LookPath("leo"); err == nil {
		return p
	}
	return ""
}

func TestIntegration_LeoVersion_Executor(t *testing.T) {
	if os.Getenv("LEO_INTEGRATION") != "1" {
		t.Skip("LEO_INTEGRATION != 1; skipping real leo execution")
	}
	bin := findLeo()
	if bin == "" {
		t.Skip("leo binary not found in PATH and LEO_BIN not set")
	}
	res := Run(context.Background(), Config{
		BinPath: bin,
		Args:    []string{"--version"},
	})
	if res.ExitCode != 0 {
		t.Fatalf("leo --version failed: exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if res.Stdout == "" && res.Stderr == "" {
		t.Fatalf("expected some output from leo --version")
	}
	t.Logf("leo --version output: %q", res.Stdout)
}
