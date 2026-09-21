# Shipyard local Dev Container.
# The toolchain (Ubuntu 24.04, Go, Node.js, python3) comes from the published
# agent base. This file only adds IDE tools: bubblewrap, tmux, sudo, the
# PostgreSQL client, and Playwright's Chromium.
#
# Rebuild when the base tag below changes, or when the Playwright lockfile
# version changes. GOTOOLCHAIN=local keeps the shell on the image toolchain.
# python3 for the model-API allowlist relay is already in the base image.
# Codex, Claude Code, and the Grok CLI are installed in the base image.
# Do not npm-install a second copy here.

ARG SHIPYARD_AGENT_BASE=ghcr.io/ralphschuler/shipyard-agent-base:v0.1
FROM ${SHIPYARD_AGENT_BASE}

ARG PLAYWRIGHT_VERSION=1.63.0

USER root

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bubblewrap \
        postgresql-client \
        sudo \
        tmux \
    && rm -rf /var/lib/apt/lists/* \
    && printf 'vscode ALL=(ALL) NOPASSWD:ALL\n' > /etc/sudoers.d/vscode \
    && chmod 0440 /etc/sudoers.d/vscode

ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
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
