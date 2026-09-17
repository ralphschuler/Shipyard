# Release Agent

`internal/release` is the constrained publication boundary for an explicitly
approved Done task. The caller constructs a `release.Request` from the
accepted run and repository target. The request fails closed unless approval,
project/task identity, repository, source/target branches, managed checkout,
and a full accepted commit SHA are present.

`Publish` first finds an open PR by exact source/target branches and the stable
marker `<!-- shipyard-release-task:<task-id> -->`, before any branch write. It
serializes matching publications per repository/branch/task in the process,
pushes the accepted commit with a normal `git push` (never force), and updates
the matching PR or creates one when none exists. If GitHub reports a create
race, it re-reads and updates the matching PR; multiple matching PRs fail
closed. The PR body includes the task/project, branches, commit, change
summary, tests, and review notes. The returned `Result.PR.URL` is the direct
audit/comment reference.

An existing PR is validated against the assigned repository and exact
source/target branches before any push. `GitPusher` also verifies that the
managed checkout's push remote resolves to the assigned GitHub repository.

`GitPusher` accepts only the exact `HEAD` commit of the managed checkout, so a
caller cannot substitute an unrelated SHA. `release.Client` receives a token
from a server-side secret assignment. It does not read service environment
variables, only accepts the canonical `https://api.github.com` API endpoint,
and does not include the token or HTTP response body in errors. Known secret
values and common token/key patterns are redacted from PR summaries and
adapter errors. The repository
currently has no canonical
Done-release event payload or release-agent secret assignment, so worker
wiring is intentionally not guessed: missing configuration must block the
run. Merge, force-push, release publication, and deployment are absent from
this package's interfaces.
