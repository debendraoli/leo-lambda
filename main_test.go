package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

// GET handling removed; no test for GET parsing

// Integration test that calls the handler to execute real leo --version
func TestIntegration_Handler_LeoVersion(t *testing.T) {
	if os.Getenv("LEO_INTEGRATION") != "1" {
		t.Skip("LEO_INTEGRATION != 1; skipping real leo execution")
	}
	// detect leo
	leoBin := os.Getenv("LEO_BIN")
	if leoBin == "" {
		if p, err := exec.LookPath("leo"); err == nil {
			leoBin = p
		}
	}
	if leoBin == "" {
		t.Skip("leo binary not found in PATH and LEO_BIN not set")
	}

	// Reload config each invocation so the LEO_BIN set here is respected
	t.Setenv("CONFIG_RELOAD_EACH_INVOCATION", "1")
	t.Setenv("LEO_BIN", leoBin)
	// keep DRY_RUN off to actually run leo
	t.Setenv("DRY_RUN", "")
	// Allow 'version' in allowed commands to avoid allowlist blocks in environments
	t.Setenv("ALLOWED_COMMANDS", "execute,version")

	body := InvokeRequest{Args: []string{"--version"}}
	b, _ := json.Marshal(body)
	req := events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"}},
		Body:           string(b),
	}
	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 from handler, got %d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerDryRun(t *testing.T) {
	// Enable dry run so command isn't actually executed
	t.Setenv("CONFIG_RELOAD_EACH_INVOCATION", "1")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("ALLOWED_COMMANDS", "execute,version")
	// Provide a small timeout
	body := InvokeRequest{Cmd: "execute --help"}
	b, _ := json.Marshal(body)
	req := events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"}},
		Body:           string(b),
	}
	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestAllowlist_BlocksDisallowed(t *testing.T) {
	t.Setenv("CONFIG_RELOAD_EACH_INVOCATION", "1")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("ALLOWED_COMMANDS", "execute")
	body := InvokeRequest{Args: []string{"build", "--flag"}}
	b, _ := json.Marshal(body)
	req := events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"}},
		Body:           string(b),
	}
	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestAllowlist_AllowsExecute(t *testing.T) {
	t.Setenv("CONFIG_RELOAD_EACH_INVOCATION", "1")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("ALLOWED_COMMANDS", "execute")
	body := InvokeRequest{Args: []string{"execute", "--help"}}
	b, _ := json.Marshal(body)
	req := events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"}},
		Body:           string(b),
	}
	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestContractAllowlist_BlocksContract(t *testing.T) {
	t.Setenv("CONFIG_RELOAD_EACH_INVOCATION", "1")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("ALLOWED_COMMANDS", "execute")
	t.Setenv("ALLOWED_CONTRACTS", "allowed_contract")
	// Attempt to execute a disallowed contract
	body := InvokeRequest{Args: []string{"execute", "disallowed_contract/token_receive_public"}}
	b, _ := json.Marshal(body)
	req := events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"}},
		Body:           string(b),
	}
	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestPrivateKeyInjection_WhenMissingInArgs(t *testing.T) {
	t.Setenv("CONFIG_RELOAD_EACH_INVOCATION", "1")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("ALLOWED_COMMANDS", "execute")
	t.Setenv("ALLOWED_CONTRACTS", "vlink_token_service_v7.aleo")
	t.Setenv("ALEO_PRIVATE_KEY", "abc123")
	body := InvokeRequest{Args: []string{"execute", "vlink_token_service_v7.aleo/token_receive_public"}}
	b, _ := json.Marshal(body)
	req := events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"}},
		Body:           string(b),
	}
	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestRPCURLEndpointInjection(t *testing.T) {
	t.Setenv("CONFIG_RELOAD_EACH_INVOCATION", "1")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("ALLOWED_COMMANDS", "execute")
	t.Setenv("ALLOWED_CONTRACTS", "vlink_token_service_v7.aleo")
	t.Setenv("ENDPOINT", "https://example-rpc")

	body := InvokeRequest{Args: []string{"execute", "vlink_token_service_v7.aleo/token_receive_public", "--network", "testnet"}}
	b, _ := json.Marshal(body)
	req := events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"}},
		Body:           string(b),
	}
	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
	}
	// In DRY_RUN mode, stdout is the echoed args. Ensure --endpoint and the endpoint value appear.
	var r Response
	if err := json.Unmarshal([]byte(resp.Body), &r); err != nil {
		t.Fatalf("invalid response json: %v", err)
	}
	if !strings.Contains(r.Stdout, "--endpoint https://example-rpc") {
		t.Fatalf("expected --endpoint injection, got stdout=%q", r.Stdout)
	}
}
