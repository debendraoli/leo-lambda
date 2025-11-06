package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	env "github.com/caarlos0/env/v11"

	"github.com/debendraoli/leo-lambda/pkg/executor"
	"github.com/debendraoli/leo-lambda/pkg/utils"
)

type Response struct {
	ExitCode  int               `json:"exitCode,omitempty"`
	Duration  float64           `json:"duration,omitempty"`
	Stdout    string            `json:"stdout,omitempty"`
	Stderr    string            `json:"stderr,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// EnvConfig is loaded at invocation time from environment variables.
type EnvConfig struct {
	AllowedCommands  []string `env:"ALLOWED_COMMANDS" envSeparator:"," envDefault:"execute"`
	AllowedContracts []string `env:"ALLOWED_CONTRACTS" envSeparator:","`
	PrivateKey       string   `env:"PRIVATE_KEY"`
	LeoBin           string   `env:"LEO_BIN" envDefault:"leo"`
	DryRun           bool     `env:"DRY_RUN" envDefault:"false"`
	MaxOutputBytes   int      `env:"MAX_OUTPUT_BYTES" envDefault:"5500000"`
	DefaultWorkdir   string   `env:"WORKDIR" envDefault:"/tmp/leo"`
	EndPoint         string   `env:"ENDPOINT" envDefault:"https://api.explorer.provable.com/v1"`
}

func loadEnvConfig() (*EnvConfig, error) {
	c := new(EnvConfig)
	return c, env.Parse(c)
}

var (
	cachedCfg  *EnvConfig
	leoVersion string
)

func init() {
	// Parse env once on cold start for performance in Lambda
	if c, err := loadEnvConfig(); err == nil {
		cachedCfg = c
		leoVersion, err = utils.GetLeoVersion()
		if err != nil {
			panic(fmt.Sprintf("failed to get leo version: %v", err))
		}
	}
}

// currentConfig returns either the cached config (default) or a freshly parsed
// config when CONFIG_RELOAD_EACH_INVOCATION=1 is set (useful for tests or dynamic reloads).
func currentConfig() (*EnvConfig, error) {
	if os.Getenv("CONFIG_RELOAD_EACH_INVOCATION") == "1" {
		return loadEnvConfig()
	}
	if cachedCfg != nil {
		return cachedCfg, nil
	}
	return loadEnvConfig()
}

func handler(ctx context.Context, req events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	cfgEnv, cfgErr := currentConfig()
	if cfgErr != nil {
		return jsonResp(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("invalid env config: %v", cfgErr)}), nil
	}

	args, err := utils.ParseArgs(req)
	if err != nil {
		return jsonResp(http.StatusBadRequest, map[string]string{"error": err.Error()}), nil
	}

	subcmd, subErr := utils.FirstSubcommand(args)
	if subErr != nil {
		return jsonResp(http.StatusBadRequest, map[string]string{"error": subErr.Error()}), nil
	}
	// Only enforce allowlist when a subcommand token exists; allow global flag-only invocations (e.g., --version)
	if subcmd != "" && len(cfgEnv.AllowedCommands) > 0 {
		if !slices.ContainsFunc(cfgEnv.AllowedCommands, func(s string) bool {
			return strings.EqualFold(strings.TrimSpace(s), subcmd)
		}) {
			return jsonResp(http.StatusForbidden, map[string]string{"error": fmt.Sprintf("command %q not allowed", subcmd)}), nil
		}
	}

	switch subcmd {
	case "execute":
		// Enforce contracts allowlist when provided (empty => allow all)
		// Inject RPC endpoint if provided via config and not present in args yet.
		if strings.TrimSpace(cfgEnv.EndPoint) != "" && !utils.HasAnyFlag(args, "--endpoint") {
			args = utils.InjectFlagValueAfterSubcommand(args, subcmd, "--endpoint", cfgEnv.EndPoint)
		}
		if len(cfgEnv.AllowedContracts) > 0 {
			if contract, _ := utils.ExtractExecuteContract(args); contract != "" {
				if !slices.Contains(cfgEnv.AllowedContracts, contract) {
					return jsonResp(http.StatusForbidden, map[string]string{"error": fmt.Sprintf("contract %q not allowed", contract)}), nil
				}
			} else {
				return jsonResp(http.StatusBadRequest, map[string]string{"error": "missing execute contract/method argument"}), nil
			}
		}
	}

	// Ensure leo uses this workdir as its home directory unless overridden.
	// Only inject for execute; global flag-only invocations like --version should remain unchanged.
	if !utils.HasAnyFlag(args, "--home") {
		args = utils.InjectFlagValueAfterSubcommand(args, subcmd, "--home", cfgEnv.DefaultWorkdir)
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
		WorkDir:        cfgEnv.DefaultWorkdir,
		MaxOutputBytes: cfgEnv.MaxOutputBytes,
	}

	start := time.Now()
	res := executor.Run(ctx, cfg)
	dur := time.Since(start)
	status := http.StatusOK

	payload := Response{
		ExitCode:  res.ExitCode,
		Duration:  dur.Seconds(),
		Stdout:    res.Stdout,
		Stderr:    res.Stderr,
		Truncated: res.Truncated,
		Meta: map[string]string{
			"version": leoVersion,
			"home":    utils.GetFlagValue(args, "--home"),
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
