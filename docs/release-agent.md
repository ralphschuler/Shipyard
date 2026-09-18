# Release Agent

`internal/release` is the constrained publication boundary for an explicitly
approved Done task. The caller constructs a `release.Request` from the
accepted run and repository target. The request fails closed unless approval,
project/task/run identity, a deterministic diff reference, repository,
source/target branches, the assigned managed checkout, and a full accepted
commit SHA are present.

`Publish` first finds an open PR by exact source/target branches and the stable
marker `<!-- shipyard-release-task:<task-id> -->`, before any branch write. It
requires a durable `PublicationLocker`; the Store implementation holds a
PostgreSQL transaction-scoped advisory lock for the repository/branch/task
identity, so retries from separate worker processes cannot both create a PR.
The lock is held through lookup, push, and create/update. A process-local
mutex remains only as a latency optimization.

It serializes matching publications per repository/branch/task in the process,
pushes the accepted commit with a normal `git push` (never force), and updates
the matching PR or creates one when none exists. If GitHub reports a create
race, it re-reads and updates the matching PR; multiple matching PRs fail
closed. The PR body includes the task/project, branches, commit, change
summary, tests, and review notes. The returned `Result.PR.URL` is the direct
audit/comment reference.

An existing PR is validated against the assigned repository and exact
source/target branches before any push. `GitPusher` also verifies that the
managed checkout's push remote resolves to the assigned GitHub repository.

`GitPusher` accepts only the exact `HEAD` commit of the assigned managed
checkout, so a caller cannot substitute an unrelated SHA or another checkout
of the same remote. `release.Client` receives a token
from a server-side secret assignment. It does not read service environment
variables, only accepts the canonical `https://api.github.com` API endpoint,
and does not include the token or HTTP response body in errors. Known secret
values and common token/key patterns are redacted from PR summaries and
adapter errors. The automation worker is the canonical Done-release entry point. It loads the
repository target, accepted run, and assigned GitHub secret server-side and
blocks if any binding is missing. Merge, force-push, release publication, and
deployment are absent from this package's interfaces.

## Done lifecycle

For `task.completed`, the automation worker requires exactly one effective
repository target and a successful, already-applied run whose non-empty
`TargetProject` exactly matches that target and whose source checkout matches
it. The GitHub token is loaded only through
`SHIPYARD_GITHUB_SECRET_ENV` and the accepted run's active agent assignment.
Missing evidence blocks the event and leaves it retryable. A successful
publication is durably keyed by task/project/run; the transaction records the
audit event and task comment together, so retries do not duplicate either.
The comment contains the direct PR URL, branches, commit, checks, and
manual-review requirement.
