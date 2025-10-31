package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	env "github.com/caarlos0/env/v11"
	shellwords "github.com/mattn/go-shellwords"

	"leo-cli-lambda/pkg/executor"
)

// Request body for POST invocations
type InvokeRequest struct {
	// If provided, these are appended to the leo binary invocation.
	Args []string `json:"args"`
	// Alternative to Args; a shell-like string (without the leading 'leo') which will be parsed into args
	Cmd string `json:"cmd"`
	// Timeout is deprecated/ignored; Lambda enforces the overall timeout.
	Timeout string `json:"timeout,omitempty"`
	// Optional working directory. Defaults to "/tmp/leo-work".
	Workdir string `json:"workdir"`
	// Optional additional environment variables to pass through
	Env map[string]string `json:"env"`
}

type Response struct {
	ExitCode   int               `json:"exitCode"`
	DurationMs int64             `json:"durationMs"`
	Stdout     string            `json:"stdout"`
	Stderr     string            `json:"stderr"`
	Truncated  bool              `json:"truncated"`
	TimedOut   bool              `json:"timedOut"`
	Meta       map[string]string `json:"meta,omitempty"`
}

// EnvConfig is loaded at invocation time from environment variables.
type EnvConfig struct {
	AllowedCommands  []string `env:"ALLOWED_COMMANDS" envSeparator:"," envDefault:"execute"`
	AllowedContracts []string `env:"ALLOWED_CONTRACTS" envSeparator:","`
	LeoPrivateKey    string   `env:"LEO_PRIVATE_KEY"`
	WalletPrivateKey string   `env:"WALLET_PRIVATE_KEY"`
	LeoBin           string   `env:"LEO_BIN" envDefault:"leo"`
	DryRun           bool     `env:"DRY_RUN" envDefault:"false"`
	MaxOutputBytes   int      `env:"MAX_OUTPUT_BYTES" envDefault:"5500000"`
	DefaultWorkdir   string   `env:"WORKDIR" envDefault:"/tmp/leo-work"`
	RPCURL           string   `env:"RPC_URL"`
}

func loadEnvConfig() (EnvConfig, error) {
	var c EnvConfig
	if err := env.Parse(&c); err != nil {
		return c, err
	}
	return c, nil
}

var (
	cfgMu       sync.RWMutex
	cachedCfg   EnvConfig
	cachedCfgOk bool
)

func init() {
	// Parse env once on cold start for performance in Lambda
	if c, err := loadEnvConfig(); err == nil {
		cfgMu.Lock()
		cachedCfg = c
		cachedCfgOk = true
		cfgMu.Unlock()
	}
}

// currentConfig returns either the cached config (default) or a freshly parsed
// config when CONFIG_RELOAD_EACH_INVOCATION=1 is set (useful for tests or dynamic reloads).
func currentConfig() (EnvConfig, error) {
	if os.Getenv("CONFIG_RELOAD_EACH_INVOCATION") == "1" {
		return loadEnvConfig()
	}
	cfgMu.RLock()
	c := cachedCfg
	ok := cachedCfgOk
	cfgMu.RUnlock()
	if ok {
		return c, nil
	}
	// Fallback: parse now if cache not populated
	return loadEnvConfig()
}

func parseArgs(req events.LambdaFunctionURLRequest, defaultWorkdir string) ([]string, string, map[string]string, error) {
	workdir := defaultWorkdir
	extraEnv := map[string]string{}

	// Prefer POST body JSON when present
	if req.RequestContext.HTTP.Method == http.MethodPost {
		var body InvokeRequest
		raw := []byte(req.Body)
		if req.IsBase64Encoded {
			dec, derr := decodeBase64(req.Body)
			if derr != nil {
				return nil, "", nil, fmt.Errorf("invalid base64 body: %w", derr)
			}
			raw = dec
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, "", nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		if strings.TrimSpace(body.Workdir) != "" {
			workdir = body.Workdir
		}
		if body.Env != nil {
			extraEnv = body.Env
		}
		if len(body.Args) > 0 {
			return body.Args, workdir, extraEnv, nil
		}
		if strings.TrimSpace(body.Cmd) != "" {
			// parse shell-like string into args
			p := shellwords.NewParser()
			p.ParseEnv = true
			args, err := p.Parse(body.Cmd)
			if err != nil {
				return nil, "", nil, fmt.Errorf("invalid cmd: %w", err)
			}
			return args, workdir, extraEnv, nil
		}
		return nil, "", nil, errors.New("missing args or cmd in request body")
	}

	// For GET and others: support query parameters
	q := req.QueryStringParameters
	if wd := q["workdir"]; strings.TrimSpace(wd) != "" {
		workdir = wd
	}

	// args as CSV or repeated is not available in LambdaFunctionURLRequest; only single values. We'll support 'args' as space-separated or comma-separated
	if cmdStr := q["cmd"]; strings.TrimSpace(cmdStr) != "" {
		p := shellwords.NewParser()
		p.ParseEnv = true
		args, err := p.Parse(cmdStr)
		if err != nil {
			return nil, "", nil, fmt.Errorf("invalid cmd: %w", err)
		}
		return args, workdir, extraEnv, nil
	}
	if aStr := q["args"]; strings.TrimSpace(aStr) != "" {
		// split on comma, then further split on whitespace inside tokens
		// Example: args=run,deploy --network testnet
		// Prefer comma first
		parts := strings.Split(aStr, ",")
		var args []string
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			sub := strings.Fields(part)
			args = append(args, sub...)
		}
		if len(args) > 0 {
			return args, workdir, extraEnv, nil
		}
	}
	return nil, "", nil, errors.New("missing args or cmd; provide ?cmd=... or body with args/cmd")
}

func handler(ctx context.Context, req events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	// Use cached config by default; allow per-invocation reload when requested
	cfgEnv, cfgErr := currentConfig()
	if cfgErr != nil {
		return jsonResp(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("invalid env config: %v", cfgErr)}), nil
	}

	args, workdir, extraEnv, err := parseArgs(req, cfgEnv.DefaultWorkdir)
	if err != nil {
		return jsonResp(http.StatusBadRequest, map[string]string{"error": err.Error()}), nil
	}

	// Enforce allowlist of subcommands
	allowed := map[string]bool{}
	if len(cfgEnv.AllowedCommands) == 0 {
		allowed["execute"] = true
	} else {
		for _, v := range cfgEnv.AllowedCommands {
			v = strings.ToLower(strings.TrimSpace(v))
			if v != "" {
				allowed[v] = true
			}
		}
	}
	subcmd, subErr := firstSubcommand(args)
	if subErr != nil {
		return jsonResp(http.StatusBadRequest, map[string]string{"error": subErr.Error()}), nil
	}
	if subcmd != "" {
		if len(allowed) > 0 && !allowed[subcmd] {
			return jsonResp(http.StatusForbidden, map[string]string{"error": fmt.Sprintf("subcommand %q not allowed", subcmd)}), nil
		}
	}

	// If the subcommand is execute, optionally enforce contract allowlist and inject private key
	if subcmd == "execute" {
		// Enforce contracts allowlist when provided
		allowedContracts := map[string]bool{}
		for _, v := range cfgEnv.AllowedContracts {
			v = strings.ToLower(strings.TrimSpace(v))
			if v != "" {
				allowedContracts[v] = true
			}

			// Inject RPC endpoint if provided via config and not present in args yet.
			if strings.TrimSpace(cfgEnv.RPCURL) != "" && !hasAnyFlag(args, "--endpoint") {
				args = injectFlagValueAfterSubcommand(args, subcmd, "--endpoint", cfgEnv.RPCURL)
			}
		}
		if len(allowedContracts) > 0 {
			if contract, _ := extractExecuteContract(args); contract != "" {
				if !allowedContracts[strings.ToLower(contract)] {
					return jsonResp(http.StatusForbidden, map[string]string{"error": fmt.Sprintf("contract %q not allowed", contract)}), nil
				}
			} else {
				return jsonResp(http.StatusBadRequest, map[string]string{"error": "missing execute contract/method argument"}), nil
			}
		}

		// Inject private key if not provided via args and available in env
		if !hasAnyFlag(args, "--private-key", "-k") {
			if pk := firstNonEmpty(cfgEnv.LeoPrivateKey, cfgEnv.WalletPrivateKey); pk != "" {
				args = injectFlagValueAfterSubcommand(args, "execute", "--private-key", pk)
			}
		}
	}

	// Ensure workdir exists
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return jsonResp(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to prepare workdir: %v", err)}), nil
	}

	// Determine binary path
	bin := cfgEnv.LeoBin

	// Optional dry-run for testing: if DRY_RUN=true, replace binary with 'echo' to simulate
	if cfgEnv.DryRun {
		bin = "echo"
	}

	cfg := executor.Config{
		BinPath:        bin,
		Args:           args,
		WorkDir:        workdir,
		MaxOutputBytes: cfgEnv.MaxOutputBytes, // keep under Lambda 6MB response limit
		ExtraEnv:       extraEnv,
	}

	start := time.Now()
	res := executor.Run(ctx, cfg)
	dur := time.Since(start)

	status := http.StatusOK
	if res.TimedOut || res.ExitCode != 0 {
		status = http.StatusInternalServerError
	}

	payload := Response{
		ExitCode:   res.ExitCode,
		DurationMs: dur.Milliseconds(),
		Stdout:     res.Stdout,
		Stderr:     res.Stderr,
		Truncated:  res.Truncated,
		TimedOut:   res.TimedOut,
		Meta: map[string]string{
			"workdir": workdir,
			"bin":     bin,
		},
	}

	return jsonResp(status, payload), nil
}

func jsonResp(status int, v any) events.LambdaFunctionURLResponse {
	b, _ := json.Marshal(v)
	return events.LambdaFunctionURLResponse{
		StatusCode:      status,
		Headers:         map[string]string{"Content-Type": "application/json"},
		Body:            string(b),
		IsBase64Encoded: false,
	}
}

func main() {
	lambda.Start(handler)
}

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// firstSubcommand returns the first non-flag token from args (case-insensitive)
// Treats "--" as end of options; the token after may be considered a subcommand if present.
func firstSubcommand(args []string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("no arguments provided")
	}
	skipFlags := true
	for i := range args {
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

// extractExecuteContract scans args to find the first token that looks like "contract/method"
// and returns the contract and method parts in lower case.
func extractExecuteContract(args []string) (contract string, method string) {
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

// hasAnyFlag checks if args contain any of the provided flags, either as separate token
// or in the form --flag=value.
func hasAnyFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

// injectFlagValueAfterSubcommand inserts a flag and value immediately after the subcommand token
// if found; otherwise it prepends them.
func injectFlagValueAfterSubcommand(args []string, subcmd, flag, value string) []string {
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
