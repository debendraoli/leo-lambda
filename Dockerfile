# syntax=docker/dockerfile:1.6

# Build Go binary for Lambda runtime bootstrap
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder
WORKDIR /usr/app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /usr/app/bootstrap . && \
    chmod +x /usr/app/bootstrap

FROM --platform=$TARGETPLATFORM public.ecr.aws/amazonlinux/amazonlinux:2023 AS leo-src

RUN dnf -y update && \
	dnf -y groupinstall "Development Tools" && \
	dnf -y install ca-certificates git pkgconf-pkg-config openssl-devel libcurl-devel zlib-devel which && \
	 dnf clean all && rm -rf /var/cache/dnf

ENV CARGO_HOME=/opt/cargo \
	RUSTUP_HOME=/opt/rustup \
	PATH=/opt/cargo/bin:$PATH
RUN curl -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain stable

WORKDIR /src
RUN git clone --recurse-submodules https://github.com/ProvableHQ/leo
WORKDIR /src/leo
# Build and install leo binary into cargo bin (avoids package ID issues)
RUN cargo install --path .

# Final Lambda runtime image (provided.al2023)
FROM --platform=$TARGETPLATFORM public.ecr.aws/lambda/provided:al2023

# Provide unversioned OpenSSL symlinks for software that dlopens libssl.so/libcrypto.so
RUN ( [ -e /usr/lib64/libssl.so ] || ( [ -e /usr/lib64/libssl.so.3 ] && ln -s /usr/lib64/libssl.so.3 /usr/lib64/libssl.so ) ) || true \
 && ( [ -e /usr/lib64/libcrypto.so ] || ( [ -e /usr/lib64/libcrypto.so.3 ] && ln -s /usr/lib64/libcrypto.so.3 /usr/lib64/libcrypto.so ) ) || true \
 && ( [ -e /lib64/libssl.so ] || ( [ -e /lib64/libssl.so.3 ] && ln -s /lib64/libssl.so.3 /lib64/libssl.so ) ) || true \
 && ( [ -e /lib64/libcrypto.so ] || ( [ -e /lib64/libcrypto.so.3 ] && ln -s /lib64/libcrypto.so.3 /lib64/libcrypto.so ) ) || true

COPY --from=builder /usr/app/bootstrap /var/runtime/bootstrap
COPY --from=leo-src /opt/cargo/bin/leo /usr/local/bin/leo

ENV PATH=/usr/local/bin:$PATH
ENV LEO_BIN=/usr/local/bin/leo
ENV AWS_EXECUTION_ENV=AWS_Lambda_go1.x

ENTRYPOINT ["/var/runtime/bootstrap"]
