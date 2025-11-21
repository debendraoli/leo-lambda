# Leo CLI Lambda Wrapper (Go)

This project exposes a minimal AWS Lambda handler that wraps the `leo` CLI. You can invoke it through a Lambda Function URL and pass the command arguments to run `leo` with faster CPU (by using larger Lambda sizes).

## Features

- Accepts args via POST JSON `{ "cmd": "..." }` or `{ "args": ["..."] }` (POST-only)
- Optional `workdir` (default `/tmp/leo`)
- Captures stdout/stderr, exit code, and reports when output is truncated (limit configurable via `MAX_OUTPUT_BYTES`, default ~5.5MB)
- Configurable binary via `LEO_BIN` env var; use `DRY_RUN=true` to echo the command for testing
- Allowlist subcommands with `ALLOWED_COMMANDS` (comma-separated, defaults to `execute`)
- Injects `--endpoint` from `ENDPOINT` env if not provided explicitly in args (default: <https://api.explorer.provable.com/v1>)
- Forces leo home to the workdir by injecting `--home <workdir>` when not set

## API (execute only)

The Lambda wraps `leo execute` and supports argument passing via POST. It also supports a contract allowlist and private key injection via environment variables.

- ALLOWED_COMMANDS: defaults to `execute` (only execute allowed). You may add `version` if you want to permit `--version` tests.
- ALLOWED_CONTRACTS: optional comma-separated list of allowed contracts (without method), e.g. `vlink_token_service_v7.aleo`.
- Private key injection: if `--private-key`/`-k` is not present in args, the handler injects `--private-key` from `PRIVATE_KEY`.

### POST example (args array)

```json
{
  "args": [
    "execute",
    "vlink_token_service_v7.aleo/token_receive_public",
    "--amount", "1",
    "--recipient", "aleo1..."
  ]
}
```

### POST example (cmd string)

```json
{
  "cmd": "execute vlink_token_service_v7.aleo/token_receive_public --amount 1 --recipient aleo1..."
}
```

### cURL example (POST)

```bash
curl -X POST \
  -H 'Content-Type: application/json' \
  -d '{
    "args": [
      "execute",
      "vlink_token_service_v7.aleo/token_receive_public",
      "--amount", "1",
      "--recipient", "aleo1..."
    ],
    "timeout": "90s"
  }' \
  "$FUNCTION_URL"
```

### Response shape

```json
{
  "exitCode": 0,
  "duration": 1.234,
  "stdout": "...",
  "stderr": "...",
  "truncated": false,
  "meta": {"home": "/tmp/leo", "version": "leo 3.2.0"}
}
```

## Go SDK

This repository ships with a lightweight Go client in [`sdk`](sdk) to help you invoke the Lambda from other services:

```go
client, err := sdk.New(os.Getenv("LEO_LAMBDA_URL"))
if err != nil {
  log.Fatal(err)
}

resp, err := client.Invoke(ctx, sdk.Request{
  Args: []string{
    "execute",
    "example.aleo/foo",
    "--amount", "1",
  },
})
if err != nil {
  log.Fatal(err)
}

fmt.Println("exit", resp.ExitCode)
fmt.Println("stdout", resp.Stdout)
```

By default the client uses `http.DefaultClient`; override it with `sdk.WithHTTPClient` when you need custom timeouts or transport settings.

Import path: `github.com/debendraoli/leo-lambda/sdk`.

## Build locally

```bash
go test ./...
go build -o bin/bootstrap .
```

## Deploy as Lambda container image

This repo includes a Dockerfile that builds the Go bootstrap and includes a pre-built `leo` binary.

1. Build and push the image to ECR.
1. Create a Lambda function from the container image; set environment variables:

- `LEO_BIN=/usr/local/bin/leo` (if not default)
- `ALLOWED_COMMANDS=execute` (default)
- `ALLOWED_CONTRACTS=vlink_token_service_v7.aleo` (example)
- `PRIVATE_KEY=<your_private_key>`
- `ENDPOINT=https://api.explorer.provable.com/v1` (optional; default shown)

1. Enable a Function URL (auth as needed) and invoke with the API above.

### Docker build (optional)

```bash
# Login to ECR first, create repo, then:
docker buildx build --platform linux/amd64,linux/arm64 -t <account>.dkr.ecr.<region>.amazonaws.com/leo-lambda:latest --push .
```

## Notes

- Lambda storage is ephemeral. Use `/tmp` for temporary files.
- If `leo` needs large datasets, consider S3 and download at runtime.
- Network and IAM permissions may be required depending on your leo usage.

## Integration tests with real leo

If you have the `leo` CLI installed locally, you can run integration tests that execute the real binary. These tests are opt-in to avoid failures in CI.

```bash
export LEO_INTEGRATION=1
# optionally set the path explicitly
# export LEO_BIN=/usr/local/bin/leo
go test ./...
```

The suite will auto-detect `LEO_BIN` or look up `leo` in `PATH`, and skip gracefully if not found.
