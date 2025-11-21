package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientValidation(t *testing.T) {
	if _, err := New(" "); err == nil {
		t.Fatalf("expected error for empty base url")
	}
}

func TestInvokeWithArgs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Args) == 0 || req.Args[0] != "leo" {
			t.Fatalf("unexpected args: %+v", req.Args)
		}
		resp := Response{ExitCode: 0, Stdout: "ok"}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	res, err := client.Invoke(context.Background(), Request{Args: []string{"leo", "--version"}})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if res.Stdout != "ok" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
}

func TestInvokeErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not allowed"})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Invoke(context.Background(), Request{Cmd: "leo --version"})
	if err == nil {
		t.Fatalf("expected error")
	}
	invokeErr, ok := err.(*InvokeError)
	if !ok {
		t.Fatalf("expected InvokeError, got %T", err)
	}
	if invokeErr.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected status: %d", invokeErr.StatusCode)
	}
	if invokeErr.Message != "not allowed" {
		t.Fatalf("unexpected message: %q", invokeErr.Message)
	}
}

func TestInvokeValidationFails(t *testing.T) {
	client, err := New("https://example.com")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.Invoke(context.Background(), Request{}); err == nil {
		t.Fatalf("expected validation error")
	}
	if _, err := client.Invoke(context.Background(), Request{Args: []string{"a"}, Cmd: "b"}); err == nil {
		t.Fatalf("expected mutually exclusive validation error")
	}
}
