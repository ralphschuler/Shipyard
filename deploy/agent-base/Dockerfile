# Canonical Shipyard agent base image.
# The Release workflow publishes this file to
# ghcr.io/ralphschuler/shipyard-agent-base. Agent runs without a project
# Dev Container pull that image. .devcontainer/Dockerfile extends it with
# IDE-only tools. Do not keep a second copy of this toolchain.
#
# Pins: Ubuntu 24.04, Go, Node.js. Playwright, PostgreSQL, sudo, and the IDE
# Compose network are not part of an agent run.
# The runner passes --user <host-uid>:<host-gid> so worktree writes stay owned
# by the service account. This image intentionally has no baked-in login.
# python3-minimal provides /usr/bin/python3 for the model-API allowlist relay.
# The host interpreter is not used.

FROM mcr.microsoft.com/devcontainers/base:ubuntu-24.04

ARG TARGETARCH
ARG GO_VERSION=1.26.8
ARG GO_SHA256_AMD64=d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b
ARG GO_SHA256_ARM64=211ffced9dcb9633a55eac6364816ec0ddd951389a740e88fa8b3337971bdda0
ARG NODE_VERSION=22.23.2
ARG NODE_SHA256_AMD64=d60acfe00a2932254bb0ad20e01b0d74397a0875595de719654b214f4b03f307
ARG NODE_SHA256_ARM64=fff4078c5def658577f92c88db7db3bc0072924bfb93fe52c1e744a54e94abb8

LABEL org.opencontainers.image.source="https://github.com/ralphschuler/Shipyard" \
      org.opencontainers.image.title="shipyard-agent-base" \
      org.opencontainers.image.description="Shipyard agent base image"

USER root

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        git \
        python3-minimal \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) go_sha="${GO_SHA256_AMD64}"; node_arch="x64"; node_sha="${NODE_SHA256_AMD64}" ;; \
        arm64) go_sha="${GO_SHA256_ARM64}"; node_arch="arm64"; node_sha="${NODE_SHA256_ARM64}" ;; \
        *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" -o /tmp/go.tgz; \
    echo "${go_sha}  /tmp/go.tgz" | sha256sum -c -; \
    rm -rf /usr/local/go; \
    tar -C /usr/local -xzf /tmp/go.tgz; \
    rm /tmp/go.tgz; \
    ln -sfn /usr/local/go/bin/go /usr/local/bin/go; \
    ln -sfn /usr/local/go/bin/gofmt /usr/local/bin/gofmt; \
    curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" -o /tmp/node.tar.xz; \
    echo "${node_sha}  /tmp/node.tar.xz" | sha256sum -c -; \
    tar -C /usr/local --strip-components=1 -xJf /tmp/node.tar.xz; \
    rm /tmp/node.tar.xz

ENV GOTOOLCHAIN=local \
    npm_config_update_notifier=false \
    PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

WORKDIR /workspace
CMD ["sleep", "infinity"]
