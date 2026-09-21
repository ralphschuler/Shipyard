# Dev Container

The Dev Container is the canonical local environment for building and testing Shipyard. It is for humans and for local IDE flows. GitHub Actions remains the release and CI path, and this directory is not a production deployment.

Open the repository in VS Code, Cursor, or Codex and choose **Reopen in Container**. The definition is [`.devcontainer/devcontainer.json`](../.devcontainer/devcontainer.json) with a Compose file, so PostgreSQL is a sibling service of the development container.

## What starts

| Piece | Pin | Role |
| --- | --- | --- |
| Ubuntu | 24.04 (`mcr.microsoft.com/devcontainers/base:ubuntu-24.04`) | Same major platform as the GitHub-hosted runners |
| Go | 1.26.8, checksum-pinned, `GOTOOLCHAIN=local` | Matches `go.mod` |
| Node.js | 22.23.2, checksum-pinned | Matches CI's Node.js 22 line |
| Playwright | 1.63.0 Chromium plus system libraries | Matches `frontend/package-lock.json`; exposed as `/usr/bin/chromium` |
| PostgreSQL | `postgres:16-alpine` | Same major image as `scripts/run-integration-tests.sh` |
| Tools | git, build-essential, curl, bubblewrap, tmux, postgresql-client | Build, race detector, and local database checks |

Rebuild the image when `go.mod`'s Go version, the Node pin in the Dockerfile, or the Playwright lockfile version changes.

The development container does not receive a container runtime socket, extra capabilities, or an `initializeCommand`. Closing the IDE stops the Compose project (`shutdownAction: stopCompose`).

## PostgreSQL

Compose starts PostgreSQL with a health check (`pg_isready`) before the development container is used. Data lives in the named volume `shipyard_dev_postgres`. The first start also creates `taskboard_agent_tests`; `.devcontainer/post-create.sh` creates that database again if the volume already existed.

The connection strings are the documented local defaults (`taskboard` / `taskboard` on database `taskboard`, and `taskboard_agent_tests` for tests). They exist only on the Compose network. The Compose file does not publish a host port, so it does not attach to PostgreSQL already listening on the host.

Inside the container:

| Variable | Value |
| --- | --- |
| `DATABASE_URL` | `postgres://taskboard:taskboard@postgres:5432/taskboard?sslmode=disable` |
| `SHIPYARD_TEST_DATABASE_URL` | `postgres://taskboard:taskboard@postgres:5432/taskboard_agent_tests?sslmode=disable` |

`go test ./...` therefore runs the opt-in PostgreSQL tests against `taskboard_agent_tests`. On the host, leave `SHIPYARD_TEST_DATABASE_URL` unset and those tests skip, which is the existing behavior. `./scripts/run-integration-tests.sh` still starts its own disposable database with Podman; that script is the host and CI path and is not required inside this container.

These values are disposable local credentials. Do not reuse them for a deployed Shipyard, and do not put production URLs, tokens, or `SHIPYARD_SECRET_KEY` into `.devcontainer/`.

## First start

`postCreateCommand` runs `.devcontainer/post-create.sh` inside the container:

1. Mark the workspace as a safe Git directory for the `vscode` user.
2. Give that user the named volume mounted at `frontend/node_modules`, so container installs stay off the host `node_modules` tree.
3. Wait until PostgreSQL accepts connections and ensure `taskboard_agent_tests` exists.
4. Run `go mod download` and a locked frontend install. When `frontend/node_modules` is the Compose volume, `npm ci` runs in a staging directory and the result is copied onto that volume, because `npm ci` would otherwise delete the mount. A host shell still runs `npm ci` directly in the workspace.

Run the script again after a lockfile or `go.mod` change: **Tasks: Run Task → Dev Container: install dependencies**, or `bash .devcontainer/post-create.sh`. Database setup runs only when `SHIPYARD_DEVCONTAINER=1`, which is set in the container and not in a host shell.

## Tasks and commands

[`.vscode/tasks.json`](../.vscode/tasks.json) maps the checks below. From the container workspace root they are:

```sh
go test ./...
go vet ./...
npm run build --prefix frontend
npm run lint --prefix frontend
npm run test:e2e --prefix frontend
bash .devcontainer/run-embedded-e2e.sh
```

`npm run test:e2e` is the Vite-backed Playwright suite. Chromium is the image browser at `CHROMIUM_PATH=/usr/bin/chromium`, which is what `frontend/playwright.config.ts` launches.

`bash .devcontainer/run-embedded-e2e.sh` follows the production-browser CI job: it builds the frontend into the embedded tree, builds a binary with `./scripts/build-taskboard.sh`, and serves it with `--embedded-app-http`. The listener is `127.0.0.1:18080` inside the container, then Playwright runs `frontend/e2e/embedded-bundle.spec.ts` with `TASKBOARD_E2E_BASE_URL`. That mode serves the embedded assets only; it does not open `DATABASE_URL`.

Bubblewrap is installed for the sandbox tests. Those tests skip when user namespaces are unavailable inside the container. This image does not add host privileges to turn them on.

## Host development

Host checkouts keep working. Nothing in this directory changes a running Shipyard service, host environment files, or the default `localhost:5432` URL used outside the container. `frontend/node_modules` in the container is the volume `shipyard_dev_node_modules`, mounted over the workspace path, so an install inside the container does not replace host dependencies.

Workspace files are mounted at `/workspaces/Shipyard` by the Dev Container CLI. The node_modules volume uses that same path.

## Agent runs on this repository

CLI agent runs (Delivery, Review, and the other CLI adapters) execute inside a container. Docker is the default runtime. `SHIPYARD_CONTAINER_RUNTIME=podman` selects Podman through the same provider interface.

When the checkout contains a policy-approved `.devcontainer`, that definition supplies the image or Dockerfile. This repository's Compose service `dev` is built from [`.devcontainer/Dockerfile`](../.devcontainer/Dockerfile) and tagged `shipyard-dev:local`. The runner does not start the IDE Compose project, does not run `postCreateCommand`, and does not install Dev Container features. Sibling services such as PostgreSQL stay part of the local IDE environment. `initializeCommand`, privileged mode, extra capabilities, and host binds outside the workspace are still rejected before any container is created.

When a project has no `.devcontainer`, the runner builds the generic fallback image [`deploy/agent-container/Dockerfile`](../deploy/agent-container/Dockerfile) (`shipyard/agent-fallback:1`). Its Ubuntu, Go, and Node.js pins match this Dev Container. Playwright and PostgreSQL are not included.

Mounts:

| Path | Mode |
| --- | --- |
| Run worktree, at the same absolute host path | Writable for Delivery. Read-only when the sandbox write mode is `readonly`. Review is switched to that mode and attached to the Delivery worktree, so Review cannot modify Delivery's files. |
| Git metadata and module cache | Read-only |
| Final-message directory | Writable, outside the worktree |
| Allowlisted host CLI login files (`auth.json` and the small companion files) | Read-only, under the container home. The host home directory is not mounted. |
| Task-assigned secrets | Process environment inside the container, via an env-file. They are not arguments and not the host process environment. The Shipyard service environment is not copied in. |

Shipyard-created bind sources (agent home, secret env-file, model-API proxy socket, relay script, and the fallback build context) are created under the workspace runtime directory: `$TASKBOARD_WORKSPACE_ROOT/runtime/container`, or `/home/agent/.taskboard-runtime/container` when the workspace root is unset. They are not placed in `/tmp` or `/var/tmp`. The taskboard unit sets `PrivateTmp=true`, so those directories exist only inside the service mount namespace and rootless `dockerd` rejects the bind. The agent home is created again immediately before `docker create`, after the image build.

`NetworkMode=none` keeps the container off the Docker bridge and allows model-API traffic only through the existing host allowlist proxy. The relay runs `python3` inside the agent image (`python3-minimal` in the fallback image and in this repository's Dev Container). A `python3` on the host is not used. `qa-network` uses the bridge. `release-bridge` still refuses a direct provider command.

API adapters still run tool commands in the host bubblewrap sandbox. That path is deprecated for CLI runs and is not used once a container starts. Host `go test`, frontend commands, and GitHub Actions do not take the agent-run path.
