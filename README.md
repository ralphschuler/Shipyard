# Shipyard

Shipyard is a self-hosted task board and agent orchestration platform for teams that want to turn software work into repeatable, observable workflows. It combines Kanban boards, repository projects, configurable agents, provider adapters, automations, live updates, and a streamable HTTP [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) endpoint in one workspace.

It is intended for developers, maintainers, and small teams running their own workflow tooling. Shipyard is currently a source-first project; deployment packaging and provider credentials are deliberately left to each installation.

## Contents

- [Features](#features)
- [Requirements](#requirements)
- [Run locally](#run-locally)
- [First workflow](#first-workflow)
- [Configuration and security](#configuration-and-security)
- [Architecture](#architecture)
- [Agents, automations, and MCP](#agents-automations-and-mcp)
- [Updates and releases](#updates-and-releases)
- [Supported platforms](#supported-platforms)
- [Development and tests](#development-and-tests)
- [Contributing](#contributing)
- [Further documentation](#further-documentation)

## Features

- Create boards from workflow templates or design a workflow with custom columns and allowed transitions.
- Link tasks to one or more repository projects and reusable project groups.
- Run local CLI agents or the OpenAI Responses adapter in isolated Git worktrees, with review and delivery gates before changes are applied.
- Trigger agents from task lifecycle events, schedules, or webhooks.
- Track runs, logs, audit events, interactions, usage, and delivery status.
- Connect MCP clients using revocable, per-account bearer tokens.
- Install and assign skills to agents, and manage provider settings from the web interface.

## Requirements

- Go 1.26 or newer.
- PostgreSQL 16 or a compatible PostgreSQL release supported by the project.
- Git, for repository projects and agent worktrees.
- Node.js and npm only when building or testing the optional frontend in `frontend/`.
- An installed agent CLI or an API account is required only for running agents; the board itself can be used without one.

## Run locally

Create a local PostgreSQL database, then start Shipyard from the repository root. The server runs migrations automatically on startup.

```sh
export DATABASE_URL='postgres://taskboard:taskboard@localhost:5432/taskboard?sslmode=disable'
export TASKBOARD_ADDR='127.0.0.1:8080'
go run ./cmd/taskboard
```

Open <http://127.0.0.1:8080/app/>. The root URL `/` permanently redirects there, preserving query parameters. On a new database, open `/setup` first to create the owner account with a password of at least 12 characters; the web interface is protected by a server-side session.

If you are working on the React frontend, use a separate terminal:

```sh
cd frontend
npm ci
npm run dev
```

The Go server serves the fingerprinted React frontend embedded in the release binary at `/app/`, which is the canonical Shipyard UI entry point. Direct extensionless client routes below `/app/` receive the app shell; missing asset paths remain 404. API, health, authentication, update, metrics, and MCP routes stay outside the UI fallback.

## First workflow

1. Open **Boards**, choose **New board**, and select the `software` template.
2. Open the board and choose **New task**. Enter a title and, optionally, a description, priority, dates, project, or labels.
3. Configure a project under **Projects** if the task should target a local repository.
4. Create an agent under **Agents**, give it a workspace and prompt, and assign only the skills it needs.
5. Configure a provider under **Settings → Provider**, then start a run from the task or add an automation under **Automations**.
6. Review the run and its diff. Apply a successful, approved run only after checking the delivery gate; otherwise discard it or continue the task.

## Configuration and security

The application reads these runtime variables:

| Variable | Purpose | Default |
| --- | --- | --- |
| `DATABASE_URL` | PostgreSQL connection string | Local PostgreSQL `taskboard` URL |
| `TASKBOARD_ADDR` | HTTP listen address | `127.0.0.1:8080` |
| `TASKBOARD_VERSION` | Displayed build version | Build metadata or `development` |
| `TASKBOARD_COMMIT_SHA` | Displayed commit identifier | `unknown` |
| `TASKBOARD_BUILD_TIME` | Displayed build timestamp | Empty |
| `TASKBOARD_CHANGELOG_PATH` | Optional local Markdown changelog fallback | `CHANGELOG.md` or `CHANGELOG.markdown` in the service working directory |
| `SHIPYARD_SECRET_KEY` | Key for encrypted application secrets | None; required for secret storage |
| `SHIPYARD_MAX_AUTOMATION_EVENT_ATTEMPTS` | Maximum retries for an automation event | `3` |

Do not commit connection strings containing real passwords, API keys, provider tokens, or private hostnames. Store provider credentials in the account's secret management UI and assign each secret explicitly to the relevant agent. Provider settings store the name of a secret environment variable, not its value. Service and database environment variables are not inherited by agent runs.

For MCP access, sign in and create a token under **Account → MCP Tokens**. The complete token is shown only once. Treat it like a password and revoke it when it is no longer needed:

```sh
curl --fail --header 'Authorization: Bearer tb_REPLACE_WITH_TOKEN' \
  --header 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  http://127.0.0.1:8080/mcp
```

Use HTTPS and an access-controlled reverse proxy when exposing Shipyard beyond localhost. The `/mcp` endpoint always uses its own bearer token; browser login does not grant MCP access.

## Architecture

Shipyard is a Go HTTP service backed by PostgreSQL. The web layer provides the React control panel at `/app/`, JSON APIs, Server-Sent Events for live updates, and the authenticated MCP transport. The store owns migrations and persistence. The automation worker watches lifecycle events and schedules, prepares isolated Git worktrees, invokes the configured provider, and records the run, logs, usage, and delivery status. The `frontend/` directory contains the React, TypeScript, and Vite application.

## Agents, automations, and MCP

An agent profile defines a prompt, workspace, provider adapter, concurrency limit, and allowed skills. A run receives the task context in its own worktree; successful changes pass through an explicit delivery gate before they can be applied to the source checkout.

Automation rules connect board events to agents. They can route tasks into success or failure columns and may require delivery approval. Schedules and webhooks provide time-based and external triggers.

MCP clients authenticate with a token and can list or create boards, tasks, projects, agents, automations, workflows, and runs. They can also move tasks, add comments, inspect run status, and apply or discard gated run worktrees. Use `tools/list` against your instance for the authoritative tool schemas.

## Updates and releases

Releases are built by the repository's GitHub Actions workflow and publish Linux artifacts for `amd64` and `arm64` with checksums. Each binary runs `npm ci`/`npm run build` first and embeds the resulting fingerprinted React app under `/app/`; no checkout `frontend/dist` is read in production. `build-info.json` records the release version and commit alongside the backend metadata. Review the workflow and the deployment examples under [`deploy/`](deploy/) before adapting them to an installation. The systemd unit supervises [`deploy/taskboard-runner.sh`](deploy/taskboard-runner.sh) as its MainPID; the runner starts the binary as a child and accepts the restart signal from the update monitor, so candidate and rollback bundles are restarted by one supervisor without a competing MainPID. After an update, run `TASKBOARD_EXPECTED_VERSION=<release> TASKBOARD_EXPECTED_COMMIT=<commit> ./deploy/verify-production.sh`; it verifies `/app/` and requires the embedded asset metadata to match the backend build exactly. For browser verification against a running production bundle, set `TASKBOARD_E2E_BASE_URL` and run `npm run test:e2e --prefix frontend`; this skips the Vite server and checks the live Go bundle, including every fingerprinted asset. `deploy/deploy-local.sh` exports these expected values from the same variables used by both builds. Update checks are optional and should be configured only with a repository and release allowlist that you control; public repositories do not require a GitHub token. Changelogs use the GitHub release body and fall back to `TASKBOARD_CHANGELOG_PATH`, `CHANGELOG.md`, or `CHANGELOG.markdown`; local files are limited to 1 MiB and are display-only.

This README documents the user-facing setup. Host-specific reverse-proxy, backup, restore, and service-unit procedures belong in deployment runbooks and must be adapted to the target environment.

## Supported platforms

The application targets platforms supported by Go and PostgreSQL. The checked-in release artifacts are Linux `amd64` and `arm64`. Agent isolation and local repository workflows additionally require Git and the tools used by the selected provider. Windows and macOS source builds may work, but are not covered by the release artifact workflow.

## Development and tests

Run Go checks from the repository root:

```sh
go test ./...
go vet ./...
```

The opt-in integration suite starts a disposable PostgreSQL container through Podman and cleans it up when it finishes:

```sh
./scripts/run-integration-tests.sh
```

For frontend work:

```sh
cd frontend
npm ci
npm run build
npm run lint
npm run test:e2e
```

The end-to-end suite uses deterministic fixtures and does not require production credentials. Its reports and failure artifacts are ignored locally.

## Contributing

1. Fork the repository and create a focused branch from `master`.
2. Explain the problem and proposed behavior in an issue before substantial changes.
3. Keep changes scoped, add or update tests, and update public documentation when behavior or configuration changes.
4. Run the relevant Go and frontend checks locally.
5. Open a pull request with a concise summary, test results, screenshots for UI changes, and any migration or security considerations.

Please do not include secrets, private infrastructure details, generated build outputs, or unrelated refactors in issues and pull requests.

## Further documentation

- [Git integration and worktree model](docs/git-integration.md)
- [Grokbot / xAI provider](docs/grokbot.md)
- [Secret handling](docs/secrets.md)
- [Frontend design system](docs/design-system.md)
- [Deployment examples](deploy/)
- [GitHub repository and issue tracker](https://github.com/ralphschuler/Shipyard)
