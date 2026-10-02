# Contribution workflow

Develop each change in a dedicated Git worktree and feature branch. Keep commits
small and independently buildable. Follow implementation, tests, then
documentation; document the behavior that exists in the same feature's commits.

## Validate before committing

Run the checks covering the change before making its commit:

| Change | Required local checks |
| --- | --- |
| Go code | `make go-check` |
| Python automation | `make scripts-check` |
| Browser application or SSR | `make go-check www-check` |
| Documentation or Zensical configuration | `make docs` |
| Component configuration | `make release-validate` |
| Every change | `make license-check` |

Before pushing or marking a pull request ready, run `make check`. It includes
license headers, multicz configuration, Python tests, Go tests with the race
detector, vet, build, gosec, frontend checks and the documentation build.
Fix failures before committing; retain the test's coverage rather than weakening
it to obtain a passing result. CI selects the affected components and packages,
while this local command checks the entire project.

## Commits and pull requests

Use English, single-line Conventional Commit subjects, at most one scope and no
breaking-change markers or footers. Sign commits with the configured contributor
identity. Stage explicit paths or hunks. Exclude local assistant instructions and
planning files from Git.

Reference the change's issue in the pull request and use the repository template.
Describe the problem, resulting behavior, validation and any material limitations.
Keep the checklist accurate, watch CI and resolve failures before merging.
Every PR commit must have a signature verified by GitHub. Rebase is the configured
merge method and the protected branch requires linear history.

User documentation lives at the root of `docs/`; architecture, contribution and
automation documentation live under `docs/development/`. README and published
pages describe implemented behavior. Unimplemented features belong in the local
reconstruction plan.
