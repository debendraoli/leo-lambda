#!/usr/bin/env bash
set -Eeuo pipefail

# test.sh - Send a sample request to the Lambda Function URL wrapping the leo CLI.
#
# Usage:
#   ./test.sh -u https://<function-id>.lambda-url.<region>.on.aws
#   FUNCTION_URL=https://<function-id>.lambda-url.<region>.on.aws ./test.sh
#   ./test.sh --print-payload   # Print JSON payload only (for inspection)
#
# Notes:
# - Requires `jq` for safe JSON construction and pretty output.
# - The handler expects `cmd` WITHOUT the leading 'leo'. This script strips it if present.

function usage() {
  cat <<'USAGE'
Send a sample POST to the Lambda Function URL for leo CLI execution.

Options:
  -u, --url URL          Function URL (or set FUNCTION_URL env var)
      --print-payload    Print the JSON payload and exit (no request)
      --no-pretty        Do not pretty-print the response with jq
  -h, --help             Show this help

Example:
  ./test.sh -u https://abc123.lambda-url.us-east-1.on.aws
USAGE
}

URL="${FUNCTION_URL:-}"
PRINT_PAYLOAD=false
PRETTY=true

while [[ $# -gt 0 ]]; do
  case "$1" in
    -u|--url)
      URL="$2"; shift 2 ;;
    --print-payload)
      PRINT_PAYLOAD=true; shift ;;
    --no-pretty)
      PRETTY=false; shift ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if ! command -v jq >/dev/null 2>&1; then
  echo "Error: jq is required but not installed. Please install jq and re-run." >&2
  exit 1
fi

CMD_FULL=$(cat <<'EOF'
leo execute vlink_token_service_v7.aleo/token_receive_public "[25u8, 103u8, 220u8, 54u8, 49u8, 180u8, 138u8, 175u8, 75u8, 36u8, 33u8, 138u8, 93u8, 185u8, 171u8, 2u8, 229u8, 54u8, 217u8, 99u8]" 5983142094692128773510225623816045070304444621008302359049788306211838130558field aleo12586w8v50dl70ku7rltpwdrmht3eaa6v0frg6669kjs49t6x5q9sfllgts 100000000u128 5u64 4649357u64 "[aleo1eslxvrgwtev68t9y6l0nxtts86exewrucgj33aw309k20tch45ps6pex24, aleo1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3ljyzc, aleo1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3ljyzc, aleo1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3ljyzc, aleo1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3ljyzc]" "[sign1frr84ayx65heh04ew0yj8448cmxwxsmvguqkj33a0etpnn82fyqvm9wzz8qrksp5ehh25dj27gdqefl5nr5mvcdqxkupwtr8j6ckwq4gn5s3nawm2kd052ekynu68mg4jty0wshn8udha3ss2zar3839q52er42ve68ugrhtzupr2apptlpejtcnm605j9zdun6ehfzqaa53ygdp30v, sign1frr84ayx65heh04ew0yj8448cmxwxsmvguqkj33a0etpnn82fyqvm9wzz8qrksp5ehh25dj27gdqefl5nr5mvcdqxkupwtr8j6ckwq4gn5s3nawm2kd052ekynu68mg4jty0wshn8udha3ss2zar3839q52er42ve68ugrhtzupr2apptlpejtcnm605j9zdun6ehfzqaa53ygdp30v, sign1frr84ayx65heh04ew0yj8448cmxwxsmvguqkj33a0etpnn82fyqvm9wzz8qrksp5ehh25dj27gdqefl5nr5mvcdqxkupwtr8j6ckwq4gn5s3nawm2kd052ekynu68mg4jty0wshn8udha3ss2zar3839q52er42ve68ugrhtzupr2apptlpejtcnm605j9zdun6ehfzqaa53ygdp30v]" 111550639260264u128 "[127u8, 188u8, 124u8, 186u8, 125u8, 226u8, 18u8, 41u8, 165u8, 227u8, 134u8, 246u8, 54u8, 216u8, 247u8, 75u8, 102u8, 174u8, 53u8, 13u8]" 150000u128 2u8 --endpoint https://api.explorer.provable.com/v1 -y --network testnet
EOF
)

# Strip leading 'leo ' if present to satisfy the Lambda handler expectation
CMD_NO_LEO="$CMD_FULL"
if [[ "$CMD_NO_LEO" == leo\ * ]]; then
  CMD_NO_LEO="${CMD_NO_LEO#leo }"
fi

PAYLOAD=$(jq -n --arg cmd "$CMD_NO_LEO" '{cmd:$cmd}')

if [[ "$PRINT_PAYLOAD" == true ]]; then
  echo "$PAYLOAD" | jq .
  exit 0
fi

if [[ -z "$URL" ]]; then
  echo "Error: Function URL not provided. Use -u/--url or set FUNCTION_URL env var." >&2
  usage
  exit 1
fi

echo "POST $URL" >&2
RESP=$(curl -sS -X POST "$URL" \
  -H 'Content-Type: application/json' \
  --data "$PAYLOAD")

if [[ "$PRETTY" == true ]]; then
  echo "$RESP" | jq .
else
  echo "$RESP"
fi
