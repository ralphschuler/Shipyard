# Shipyard local Dev Container.
# Pins follow the repository: Go from go.mod, Node.js 22 from CI, Ubuntu 24.04
# from the GitHub-hosted runners, and Playwright from frontend/package-lock.json.
# PostgreSQL 16 is a sibling Compose service, not a package in this image.
#
# Rebuild this image when go.mod's Go version, the Node pin below, or the
# Playwright lockfile version changes. GOTOOLCHAIN=local keeps the shell on
# the image toolchain.
# python3-minimal provides /usr/bin/python3 for the model-API allowlist relay
# when an agent run builds this image. The host interpreter is not used.

FROM mcr.microsoft.com/devcontainers/base:ubuntu-24.04

ARG TARGETARCH
ARG GO_VERSION=1.26.8
ARG GO_SHA256_AMD64=d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b
ARG GO_SHA256_ARM64=211ffced9dcb9633a55eac6364816ec0ddd951389a740e88fa8b3337971bdda0
ARG NODE_VERSION=22.23.2
ARG NODE_SHA256_AMD64=d60acfe00a2932254bb0ad20e01b0d74397a0875595de719654b214f4b03f307
ARG NODE_SHA256_ARM64=fff4078c5def658577f92c88db7db3bc0072924bfb93fe52c1e744a54e94abb8
ARG PLAYWRIGHT_VERSION=1.63.0

USER root

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        bubblewrap \
        ca-certificates \
        curl \
        git \
        postgresql-client \
        python3-minimal \
        sudo \
        tmux \
        xz-utils \
    && rm -rf /var/lib/apt/lists/* \
    && printf 'vscode ALL=(ALL) NOPASSWD:ALL\n' > /etc/sudoers.d/vscode \
    && chmod 0440 /etc/sudoers.d/vscode

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
    PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
    CHROMIUM_PATH=/usr/bin/chromium \
    npm_config_update_notifier=false

# Playwright's Chromium build and its system libraries live in the image so
# `npm run test:e2e` can use /usr/bin/chromium, matching frontend/playwright.config.ts.
RUN set -eux; \
    npm install --global "playwright@${PLAYWRIGHT_VERSION}"; \
    playwright install --with-deps chromium; \
    browser="$(find /ms-playwright -type f -name chrome -path '*chrome-linux*' | head -n 1)"; \
    test -n "${browser}"; \
    ln -sfn "${browser}" /usr/local/bin/chromium; \
    ln -sfn /usr/local/bin/chromium /usr/bin/chromium; \
    chmod -R a+rX /ms-playwright

USER vscode
