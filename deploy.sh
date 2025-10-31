#!/usr/bin/env bash
set -euo pipefail

# deploy.sh - Build and deploy the leo-lambda container to AWS Lambda (Function URL compatible)
#
# Requirements:
# - AWS CLI v2 configured with credentials
# - Docker with buildx
# - jq (optional; used to merge environment variables)
#
# Usage (environment variables):
#   AWS_REGION=us-east-1 \
#   AWS_ACCOUNT_ID=123456789012 \
#   AWS_PROFILE=my-profile \  # optional; applies to all AWS CLI calls
#   FUNCTION_NAME=leo-lambda \
#   REPO_NAME=leo-lambda \
#   IMAGE_TAG=<git_sha_or_timestamp> \
#   PLATFORMS="linux/amd64,linux/arm64" \
#   TAG_LATEST=1 \
#   LEO_VERSION=v3.2.0 \
#   LEO_ASSET=leo-mainnet-x86_64-unknown-linux-gnu.zip \
#   LAMBDA_ROLE_ARN=arn:aws:iam::123456789012:role/lambda-execution-role \  # only needed when creating function
#   # Optional Lambda env vars:
#   ALLOWED_COMMANDS=execute \
#   ALLOWED_CONTRACTS=vlink_token_service_v7.aleo \
#   LEO_PRIVATE_KEY=your_private_key \
#   LEO_BIN=/usr/local/bin/leo \
#   MEMORY_SIZE=2048 \
#   TIMEOUT=900 \
#   FUNCTION_URL_AUTH=AWS_IAM            # or NONE
#   ./deploy.sh
#
# Notes:
# - If FUNCTION_NAME doesn't exist and LAMBDA_ROLE_ARN is provided, the script will create it.
# - If jq is available, environment variables are merged with existing ones; otherwise only
#   the provided ones are set (others are left unchanged if you skip config update).

echo "==> Starting deployment"

# Resolve region and account id if not provided

# Optional profile support via helper
aws_cli() {
  if [[ -n "${AWS_PROFILE:-}" ]]; then
    aws --profile "${AWS_PROFILE}" "$@"
  else
    aws "$@"
  fi
}

AWS_REGION="${AWS_REGION:-$(aws_cli configure get region 2>/dev/null || true)}"
if [[ -z "${AWS_REGION}" ]]; then
  echo "AWS_REGION is required (or configure a default with 'aws configure')." >&2
  exit 1
fi

AWS_ACCOUNT_ID="${AWS_ACCOUNT_ID:-}"
if [[ -z "${AWS_ACCOUNT_ID}" ]]; then
  AWS_ACCOUNT_ID="$(aws_cli sts get-caller-identity --query 'Account' --output text)"
fi

FUNCTION_NAME="${FUNCTION_NAME:-leo-lambda}"
REPO_NAME="${REPO_NAME:-leo-lambda}"

# Generate a default tag from git or timestamp
if [[ -z "${IMAGE_TAG:-}" ]]; then
  if command -v git >/dev/null 2>&1 && git rev-parse --short HEAD >/dev/null 2>&1; then
    IMAGE_TAG="$(git rev-parse --short HEAD)"
  else
    IMAGE_TAG="$(date +%Y%m%d%H%M%S)"
  fi
fi
PLATFORMS="${PLATFORMS:-linux/amd64}"
TAG_LATEST="${TAG_LATEST:-1}"
LEO_VERSION="${LEO_VERSION:-}"
LEO_ASSET="${LEO_ASSET:-}"

# Function URL and CORS defaults
CREATE_FUNCTION_URL="${CREATE_FUNCTION_URL:-1}"
FUNCTION_URL_AUTH="${FUNCTION_URL_AUTH:-NONE}"
CORS_ALLOW_ORIGINS="${CORS_ALLOW_ORIGINS:-[\"*\"]}"
CORS_ALLOW_METHODS="${CORS_ALLOW_METHODS:-[\"GET\",\"POST\"]}"
CORS_ALLOW_HEADERS="${CORS_ALLOW_HEADERS:-[\"*\"]}"

ECR_URI="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/${REPO_NAME}"

echo "==> Region: ${AWS_REGION}"
echo "==> Account: ${AWS_ACCOUNT_ID}"
echo "==> Function: ${FUNCTION_NAME}"
echo "==> ECR repo: ${ECR_URI}"
echo "==> Image tag: ${IMAGE_TAG}"
echo "==> Platforms: ${PLATFORMS}"
if [[ -n "${LEO_VERSION}" ]]; then echo "==> LEO_VERSION: ${LEO_VERSION}"; fi
if [[ -n "${LEO_ASSET}" ]]; then echo "==> LEO_ASSET:   ${LEO_ASSET}"; fi

echo "==> Ensuring ECR repository exists"
if ! aws_cli ecr describe-repositories --repository-names "${REPO_NAME}" --region "${AWS_REGION}" >/dev/null 2>&1; then
  aws_cli ecr create-repository --repository-name "${REPO_NAME}" --region "${AWS_REGION}" >/dev/null
  echo "Created ECR repository ${REPO_NAME}"
fi

echo "==> Logging in to ECR"
aws_cli ecr get-login-password --region "${AWS_REGION}" | docker login --username AWS --password-stdin "${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"

echo "==> Ensuring a buildx builder is available"
if ! docker buildx inspect multiarch >/dev/null 2>&1; then
  docker buildx create --name multiarch --use >/dev/null
fi

# Ensure an IAM role exists if we need to create a new Lambda function
# Returns the Role ARN on stdout
ensure_iam_role() {
  local role_name
  role_name="${ROLE_NAME:-lambda-basic-execution-role}"
  echo "==> Ensuring IAM role ${role_name} exists" >&2
  if ! aws_cli iam get-role --role-name "${role_name}" >/dev/null 2>&1; then
    echo "Creating IAM role ${role_name}" >&2
    aws_cli iam create-role \
      --role-name "${role_name}" \
      --assume-role-policy-document '{
        "Version": "2012-10-17",
        "Statement": [
          {
            "Effect": "Allow",
            "Principal": { "Service": "lambda.amazonaws.com" },
            "Action": "sts:AssumeRole"
          }
        ]
      }' >/dev/null
    aws_cli iam attach-role-policy \
      --role-name "${role_name}" \
      --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole >/dev/null
    # VPC policy is optional; attach if available (ignore failures)
    aws_cli iam attach-role-policy \
      --role-name "${role_name}" \
      --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole >/dev/null || true
    # Give IAM a moment to propagate
    sleep 5
  fi
  aws_cli iam get-role --role-name "${role_name}" --query 'Role.Arn' --output text
}

# Resolve target platform and Lambda architecture (single-arch image required for Lambda)
if [[ -n "${LAMBDA_ARCH:-}" ]]; then
  case "${LAMBDA_ARCH}" in
    arm64) SELECTED_PLATFORM="linux/arm64" ;;
    x86_64|amd64) SELECTED_PLATFORM="linux/amd64" ; LAMBDA_ARCH="x86_64" ;;
    *) echo "Unsupported LAMBDA_ARCH: ${LAMBDA_ARCH}. Use arm64 or x86_64." >&2; exit 1 ;;
  esac
else
  if [[ "${PLATFORMS}" == *"arm64"* && "${PLATFORMS}" != *"amd64"* ]]; then
    SELECTED_PLATFORM="linux/arm64"; LAMBDA_ARCH="arm64"
  else
    SELECTED_PLATFORM="linux/amd64"; LAMBDA_ARCH="x86_64"
  fi
fi

echo "==> Building and pushing image"
echo "Selected platform: ${SELECTED_PLATFORM} (Lambda arch: ${LAMBDA_ARCH})"
export BUILDX_NO_DEFAULT_ATTESTATIONS=1
BUILD_ARGS=()
if [[ -n "${LEO_VERSION}" ]]; then BUILD_ARGS+=(--build-arg "LEO_VERSION=${LEO_VERSION}"); fi
if [[ -n "${LEO_ASSET}" ]]; then BUILD_ARGS+=(--build-arg "LEO_ASSET=${LEO_ASSET}"); fi

COMMON_BUILD_ARGS=(--platform "${SELECTED_PLATFORM}" -t "${ECR_URI}:${IMAGE_TAG}")
if [[ "${TAG_LATEST}" == "1" ]]; then
  COMMON_BUILD_ARGS+=(-t "${ECR_URI}:latest")
fi

# Compose final build args, guarding empty BUILD_ARGS expansion under set -u
FINAL_BUILD_ARGS=("${COMMON_BUILD_ARGS[@]}")
if (( ${#BUILD_ARGS[@]:-0} > 0 )); then
  FINAL_BUILD_ARGS+=("${BUILD_ARGS[@]}")
fi

# Build and push with Docker media types; fallback if option unsupported
build_and_push_image() {
  if docker buildx build \
    "${FINAL_BUILD_ARGS[@]}" \
    --output=type=registry,oci-mediatypes=false \
    .; then
    return 0
  fi
  echo "Falling back to --push with provenance/sbom disabled" >&2
  docker buildx build \
    "${FINAL_BUILD_ARGS[@]}" \
    --push \
    --provenance=false \
    --sbom=false \
    .
}

build_and_push_image

IMAGE_URI="${ECR_URI}:${IMAGE_TAG}"
LATEST_URI="${ECR_URI}:latest"

echo "==> Checking if Lambda function exists"
if aws_cli lambda get-function --function-name "${FUNCTION_NAME}" --region "${AWS_REGION}" >/dev/null 2>&1; then
  echo "==> Updating function code"
  aws_cli lambda update-function-code \
    --function-name "${FUNCTION_NAME}" \
    --image-uri "${IMAGE_URI}" \
    --region "${AWS_REGION}" >/dev/null
  aws_cli lambda wait function-updated --function-name "${FUNCTION_NAME}" --region "${AWS_REGION}"
else
  if [[ -z "${LAMBDA_ROLE_ARN:-}" ]]; then
    # Create or fetch a basic execution role compatible with Lambda
    LAMBDA_ROLE_ARN="$(ensure_iam_role)"
  fi
  echo "==> Creating Lambda function ${FUNCTION_NAME}"
  aws_cli lambda create-function \
    --function-name "${FUNCTION_NAME}" \
    --package-type Image \
    --code ImageUri="${IMAGE_URI}" \
    --role "${LAMBDA_ROLE_ARN}" \
    --architectures "${LAMBDA_ARCH}" \
    --memory-size "${MEMORY_SIZE:-2048}" \
    --timeout "${TIMEOUT:-900}" \
    --region "${AWS_REGION}" >/dev/null
  aws_cli lambda wait function-active --function-name "${FUNCTION_NAME}" --region "${AWS_REGION}"
fi

# Merge or set environment variables when any of the known vars are provided
KNOWN_ENV_KEYS=(ALLOWED_COMMANDS ALLOWED_CONTRACTS LEO_PRIVATE_KEY LEO_BIN)
PROVIDED_COUNT=0
for k in "${KNOWN_ENV_KEYS[@]}"; do
  if [[ -n "${!k:-}" ]]; then
    PROVIDED_COUNT=$((PROVIDED_COUNT+1))
  fi
done

if (( PROVIDED_COUNT > 0 )); then
  echo "==> Updating environment variables"
  if command -v jq >/dev/null 2>&1; then
    EXISTING=$(aws_cli lambda get-function-configuration --function-name "${FUNCTION_NAME}" --region "${AWS_REGION}" --query 'Environment.Variables' --output json)
    # Build JSON for provided vars
    PROVIDED_JSON='{}'
    for k in "${KNOWN_ENV_KEYS[@]}"; do
      v="${!k:-}"
      if [[ -n "$v" ]]; then
        PROVIDED_JSON=$(echo "$PROVIDED_JSON" | jq --arg k "$k" --arg v "$v" '. + {($k): $v}')
      fi
    done
    MERGED=$(jq -n --argjson a "${EXISTING:-{}}" --argjson b "${PROVIDED_JSON}" '$a + $b')
    aws_cli lambda update-function-configuration \
      --function-name "${FUNCTION_NAME}" \
      --environment "Variables=${MERGED}" \
      --memory-size "${MEMORY_SIZE:-2048}" \
      --timeout "${TIMEOUT:-900}" \
      --region "${AWS_REGION}" >/dev/null
  else
    echo "jq not found; setting only provided variables (may overwrite others if present)."
    # Construct Variables= key=value,...
    VARS=""
    for k in "${KNOWN_ENV_KEYS[@]}"; do
      v="${!k:-}"
      [[ -z "$v" ]] && continue
      if [[ -n "$VARS" ]]; then VARS+=" "; fi
      VARS+="$k=$v"
    done
    aws_cli lambda update-function-configuration \
      --function-name "${FUNCTION_NAME}" \
      --environment "Variables={${VARS// /,}}" \
      --memory-size "${MEMORY_SIZE:-2048}" \
      --timeout "${TIMEOUT:-900}" \
      --region "${AWS_REGION}" >/dev/null
  fi
  aws_cli lambda wait function-updated --function-name "${FUNCTION_NAME}" --region "${AWS_REGION}"
fi

# Ensure a Function URL exists (optional; default enabled) with CORS
if [[ "${CREATE_FUNCTION_URL}" == "1" ]]; then
  echo "==> Ensuring Function URL exists (auth: ${FUNCTION_URL_AUTH})"
  URL_CFG_JSON=$(aws_cli lambda list-function-url-configs --function-name "${FUNCTION_NAME}" --region "${AWS_REGION}" --output json 2>/dev/null || echo '{}')
  URL_COUNT=$(echo "${URL_CFG_JSON}" | jq '(.FunctionUrlConfigs // []) | length' 2>/dev/null || echo 0)
  if [[ "${URL_COUNT}" == "0" ]]; then
    aws_cli lambda create-function-url-config \
      --function-name "${FUNCTION_NAME}" \
      --auth-type "${FUNCTION_URL_AUTH}" \
      --cors "AllowOrigins=${CORS_ALLOW_ORIGINS},AllowMethods=${CORS_ALLOW_METHODS},AllowHeaders=${CORS_ALLOW_HEADERS}" \
      --region "${AWS_REGION}" >/dev/null
  else
    # Update existing config to desired auth and CORS
    aws_cli lambda update-function-url-config \
      --function-name "${FUNCTION_NAME}" \
      --auth-type "${FUNCTION_URL_AUTH}" \
      --cors "AllowOrigins=${CORS_ALLOW_ORIGINS},AllowMethods=${CORS_ALLOW_METHODS},AllowHeaders=${CORS_ALLOW_HEADERS}" \
      --region "${AWS_REGION}" >/dev/null
  fi
  # When using auth NONE, grant public invoke permission (idempotent)
  if [[ "${FUNCTION_URL_AUTH}" == "NONE" ]]; then
    aws_cli lambda add-permission \
      --function-name "${FUNCTION_NAME}" \
      --statement-id function-url-public \
      --action lambda:InvokeFunctionUrl \
      --principal '*' \
      --function-url-auth-type NONE \
      --region "${AWS_REGION}" >/dev/null || true
  fi
fi

echo "==> Deployment complete"
echo "Function: ${FUNCTION_NAME}"
echo "Image:    ${IMAGE_URI}"
if [[ "${TAG_LATEST}" == "1" ]]; then echo "Latest:   ${LATEST_URI}"; fi
if URL=$(aws_cli lambda list-function-url-configs --function-name "${FUNCTION_NAME}" --region "${AWS_REGION}" --query 'FunctionUrlConfigs[0].FunctionUrl' --output text 2>/dev/null); then
  if [[ "$URL" != "None" ]]; then
    echo "URL:      ${URL}"
  fi
fi