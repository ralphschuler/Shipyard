# Release Agent

`internal/release` is the constrained publication boundary for an explicitly
approved Done task. The caller constructs a `release.Request` from the
accepted run and repository target. The request fails closed unless approval,
project/task identity, repository, source/target branches, managed checkout,
and a full accepted commit SHA are present.

`Publish` pushes the accepted commit with a normal `git push` (never force),
then finds an open PR by exact source/target branches and the stable marker
`<!-- shipyard-release-task:<task-id> -->`. It updates that PR or creates one
when none exists; multiple matching PRs fail closed. The PR body includes the
task/project, branches, commit, change summary, tests, and review notes. The
returned `Result.PR.URL` is the direct audit/comment reference.

`release.Client` receives a token from a server-side secret assignment. It does
not read service environment variables and does not include the token or HTTP
response body in errors. The repository currently has no canonical
Done-release event payload or release-agent secret assignment, so worker
wiring is intentionally not guessed: missing configuration must block the
run. Merge, force-push, release publication, and deployment are absent from
this package's interfaces.
