# Shipyard module boundaries

This document describes the incremental target architecture for the React web app and Go HTTP layer. It is intentionally compatible with the current routes and API contracts.

## Current to target mapping

| Current area | Target boundary | Migration phase |
| --- | --- | --- |
| `src/App.tsx` API helpers and refresh routing | `src/api/client.ts` | 1 (this change) |
| `src/App.tsx` dashboard and memory branches | `src/features/dashboard.tsx`, `src/features/memory.tsx` | 1 (already isolated) |
| `src/App.tsx` settings, boards, tasks, runs | `src/features/<domain>/` pages and hooks | 2 |
| `internal/web/web.go` dashboard API methods | `internal/web/dashboard_handler.go` + `DashboardReader` | 1 (this change) |
| Store calls from handlers | `internal/store` interfaces per workflow | 3 |
| Cross-cutting validation/error/serialization | focused `internal/web` helpers | 3 |

## Import and package rules

- `App.tsx` owns shell navigation only; feature code should be imported through a feature entry point.
- `api/` may depend on browser APIs and React hooks, but never on feature modules.
- Features may depend on `api/`, `components/`, `lib/`, and `i18n`; they must not import another feature's internals.
- Go handlers depend on domain contracts and small interfaces, never on database implementation details beyond constructor wiring.
- Repositories and integrations depend on domain types; domain packages do not depend on HTTP or storage.

## Migration and rollback order

1. Extract shared API behavior and one representative route (dashboard).
2. Extract remaining route-level features one at a time, keeping endpoint payloads unchanged.
3. Introduce workflow/repository interfaces around the highest-conflict backend paths.
4. Delete compatibility code only after focused and browser tests cover the moved path.

Each phase is independently revertible: restore the previous import/handler wiring while retaining the unchanged API contract and persistence code.
