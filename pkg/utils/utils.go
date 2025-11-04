// Package utils contains small parsing and CLI-args manipulation helpers used by the Lambda handler.
package utils

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func findLeo() string {
	if p := os.Getenv("LEO_BIN"); p != "" {
		return p
	}
	if p, err := exec.LookPath("leo"); err == nil {
		return p
	}
	return ""
}

// DecodeBase64 decodes a standard base64-encoded string.
func DecodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// FirstSubcommand returns the first non-flag token from args (case-insensitive).
// Treats "--" as end of options; the token after may be considered a subcommand if present.
func FirstSubcommand(args []string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("no arguments provided")
	}
	skipFlags := true
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if skipFlags {
			if tok == "--" {
				skipFlags = false
				continue
			}
			if strings.HasPrefix(tok, "-") {
				continue
			}
		}
		if strings.TrimSpace(tok) != "" {
			return strings.ToLower(tok), nil
		}
	}
	// Only flags present; treat as no subcommand and allow caller to decide
	return "", nil
}

// ExtractExecuteContract scans args to find the first token that looks like "contract/method"
// and returns the contract and method parts in lower case.
func ExtractExecuteContract(args []string) (contract string, method string) {
	for _, tok := range args {
		if strings.HasPrefix(tok, "-") || strings.TrimSpace(tok) == "" {
			continue
		}
		// Ignore URL-like tokens (e.g., https://...), which contain slashes but are not contracts
		if strings.Contains(tok, "://") {
			continue
		}
		if i := strings.Index(tok, "/"); i > 0 && i < len(tok)-1 {
			c := strings.ToLower(strings.TrimSpace(tok[:i]))
			m := strings.ToLower(strings.TrimSpace(tok[i+1:]))
			return c, m
		}
	}
	return "", ""
}

// HasAnyFlag checks if args contain any of the provided flags, either as separate token
// or in the form --flag=value.
func HasAnyFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

// InjectFlagValueAfterSubcommand inserts a flag and value immediately after the subcommand token
// if found; otherwise it prepends them.
func InjectFlagValueAfterSubcommand(args []string, subcmd, flag, value string) []string {
	idx := -1
	// find first non-flag token (subcommand), but specifically match on provided subcmd
	skipFlags := true
	for i, tok := range args {
		if skipFlags {
			if tok == "--" {
				skipFlags = false
				continue
			}
			if strings.HasPrefix(tok, "-") {
				continue
			}
		}
		if strings.EqualFold(tok, subcmd) {
			idx = i
			break
		}
	}
	if idx >= 0 {
		// insert after idx
		out := make([]string, 0, len(args)+2)
		out = append(out, args[:idx+1]...)
		out = append(out, flag, value)
		out = append(out, args[idx+1:]...)
		return out
	}
	// prepend by default
	return append([]string{flag, value}, args...)
}

// FirstNonEmpty returns the first non-empty trimmed string from vals.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// GetFlagValue returns the value of a flag from args in either forms:
//
//	--flag value
//	--flag=value
//
// It returns empty string if not found.
func GetFlagValue(args []string, flag string) string {
	for i := range args {
		a := args[i]
		if a == flag {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if after, ok := strings.CutPrefix(a, flag+"="); ok {
			return after
		}
	}
	return ""
}

// Run runs the arbitrary command with given args and returns the result.
func RunLeoBin(args ...string) (string, error) {
	bin := findLeo()
	cmd := exec.Command(bin, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("leo command failed: %v, stderr=%s", err, errBuf.String())
	}
	return outBuf.String(), nil
}

// GetLeoVersion returns the leo version
func GetLeoVersion() (string, error) {
	version, err := RunLeoBin("--version")
	if err != nil {
		return "", err
	}
	parts := strings.Fields(version)
	if len(parts) >= 2 {
		return parts[1], nil
	}
	return "", fmt.Errorf("unexpected version output: %q", version)
}
