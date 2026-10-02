# GitHub automation

Repository configuration is declared in `.github/repository.toml`. It covers
the labels used by issue templates and Dependabot, signing identity variables,
required signing secrets, workflow token permissions and the `main` ruleset.

## Check the repository

Authenticate `gh` with an account that can read repository settings and Actions
secrets, then run:

```console
python3 scripts/init_github.py check --repo goabonga/cloud
```

`check` performs reads only. It exits with status 1 when managed configuration
is missing or differs. GitHub exposes secret names, not secret values: presence
checks cannot prove that a token has the right scopes or that a private key
matches the configured signing identity.

## Tokens by responsibility

Create separate fine-grained PATs for the owner `goabonga`, restricted to the
`cloud` repository. Save them as **Actions repository secrets**, not Dependabot
secrets: these workflows execute in a trusted Actions context.

| Secret | Responsibility | Repository permissions |
| --- | --- | --- |
| `DEPENDABOT_PAT` | Push rewritten dependency commits and read the current PR | Contents: write; Pull requests: read; Workflows: write |
| `AUTO_MERGE_PAT` | Check authorization, push signed commits, fast-forward `main`, report failures | Contents: write; Pull requests: read; Workflows: write; Issues: write |
| `RELEASE_PAT` | Push signed documentation version commits and tags; create GitHub releases | Contents: write |

PAT setup instructions are declared under `[tokens]` in
`.github/repository.toml`. Both `check` and `init` display the instructions for
missing PATs: creation URL, resource owner, selected repository, permission
levels and the token owner's required repository role. These declarations are
guidance; the script cannot inspect permissions inside an existing secret.

Metadata read access is implicit. The merge and release tokens must belong to the maintainer
whose repository role is allowed by the ruleset's administrator bypass. They do
not need permission to change policies. Tracking issue creation and PR linking
use the workflow's own `GITHUB_TOKEN`, with explicitly declared permissions.
The dependency token is used only for the signed push and its PR validation.
The merge workflow reads check runs and commit statuses using its own
`GITHUB_TOKEN`, with `checks: read` and `statuses: read`. Its dedicated PAT handles
PR authorization, Git pushes and issue comments. Select only the PAT permissions
in the table; the workflow token supplies the CI read permissions.
See GitHub's [PAT permission reference](https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens),
[check-run permissions](https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference)
and [collaborator permission checks](https://docs.github.com/en/rest/collaborators/collaborators#get-repository-permissions-for-a-user).

For local initialization, use a separate administrative `gh` login. `check` needs
Administration, Secrets, Variables and Issues read access; `init` needs
write access for those settings. No automation PAT needs Secrets or Variables
access. GitHub's [PAT setup guide](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)
explains creation, repository selection and expiration.

## Initialize or repair configuration

Review `.github/repository.toml`, then apply it:

```console
python3 scripts/init_github.py init --repo goabonga/cloud
```

Existing secrets are preserved unless their names are explicitly supplied as
environment variables. Missing required secrets must be provided this way before
initialization can proceed. Values are passed to `gh secret set` through standard
input; they are never stored in the configuration or printed. Initialization
requires write access to repository administration, Actions secrets and variables.

`DEPENDABOT_PAT`, `AUTO_MERGE_PAT` and `RELEASE_PAT` must be configured before
enabling their workflows on `main`. Each workflow uses only its dedicated token. Supply each value through its matching environment variable when running
`init`, or store it directly with `gh secret set NAME --repo goabonga/cloud`.
Creating a PAT is a separate GitHub account operation; the script stores supplied
values but cannot generate tokens or recover existing secret values.

If required secrets are missing, `init` exits with status 1 before changing any
repository configuration and lists the commands needed to supply them. Create
each dedicated PAT with the permissions listed above, then paste its value into
the GitHub CLI's interactive prompt:

```console
gh secret set DEPENDABOT_PAT --repo goabonga/cloud
gh secret set AUTO_MERGE_PAT --repo goabonga/cloud
python3 scripts/init_github.py init --repo goabonga/cloud
python3 scripts/init_github.py check --repo goabonga/cloud
```

See the [GitHub CLI secret command](https://cli.github.com/manual/gh_secret_set).
The initializer preserves existing secrets; it cannot retrieve their values or
automatically create personal access tokens.

The command updates existing managed labels, variables and rulesets rather than
creating duplicates. It leaves unrelated labels, variables, secrets and rulesets
alone. A failed run can be retried: it reads the actual remote configuration again
and applies the remaining differences. The named managed ruleset is authoritative;
review its complete definition before applying changes to an existing repository.

The administrator bypass is retained from the source so the signed merge and
release workflows can fast-forward `main`. The merge workflow verifies the
maintainer's authorization, the current PR head and successful CI before and
after signing. The release workflow runs after successful CI validation
and refuses to release an unvalidated newer commit.

## Dependabot tracking issues

The rewrite workflow creates one tracking issue per PR. Its durable identity
includes the repository and PR number and is stored in the issue body. It scans
all pages of open and closed issues before creating anything, including legacy
tracking issues from the source. Changing the PR title or replacing its description
does not create another issue.

Issue creation and PR linking happen before pushing signed commits. If a response
is lost or linking fails, rerunning the workflow reuses the persisted issue and
repairs the link. `Closes #…` in the PR description closes the issue on merge;
the script also repairs closure if merging races with link repair. Existing
duplicates are reported and the oldest issue is reused; they are not deleted.

## Changed components in CI

The pipeline separates Plumber, multicz component detection, license headers,
script validation, Go validation, frontend validation and documentation validation
into independent jobs. Plumber runs first; license headers and component detection
then run in parallel. Branch protection requires these seven checks directly:
`audit`, `detect changed components`, `check license headers`, `validate scripts`,
`validate go`, `validate frontend` and `validate documentation`. There is no
aggregate validation job. Before releasing, the release script rejects failed,
cancelled or unexpectedly skipped checks.

The detection job runs `python3 scripts/ci_automation.py changed-components --ci`
with full Git history. It calls `multicz changed --output json --since <base>`:
PRs compare against their base SHA, pushes against the previous SHA. The changed
component names appear in the log, the job summary and
`needs.components.outputs.changed`. This output includes release dependencies.
`needs.components.outputs.checks` lists the components to validate, while `go`
lists selected Go executables and `files` lists the changed files. Documentation
release cascades do not trigger documentation validation unless documentation
files actually changed. When no safe ancestor exists (initial branch,
rewritten history or manual run), all components are validated. An empty list is
reported explicitly. Without `--ci`, the command compares each component against
its latest tag; `--since <git-reference>` allows an explicit comparison.

`cloud-scripts` owns `scripts/**` and `pytest.ini`. Its syntax checks and pytest
job run only when these files change. Documentation validation runs only for
`docs/**`, `zensical.toml` or the shared logo `assets/cloud.svg`. Unchanged
components skip their validation jobs; release authorization accepts those skips
only when component detection confirms that the corresponding checks are not
needed. License headers are checked on every run.

The scripts use `scripts/pyproject.toml` and `scripts/uv.lock`; uv installs the
locked runtime dependencies and pytest development group. The component starts
at `0.0.0` and multicz manages `project.version` and `scripts/CHANGELOG.md`. Its
post-bump hook refreshes the lockfile with `uv lock --project scripts`. Automatic
releases create a signed component tag and GitHub release; they do not publish
a Python package to PyPI.

Go components register their actual command packages under
`plugins.go-deps.packages`. The plugin resolves internal ownership with
`go list -deps`; the detection job verifies this graph before consulting multicz,
so an unavailable or broken Go toolchain cannot silently skip its consumers.
Shared Go module changes select all Go components through
`overlap_policy = "all"`. The Go job combines the selected import graphs, tests
changed packages and their consumers, and checks each shared package once. It
runs formatting, vet, race tests and gosec, then builds the selected executables.
Without a comparison base, it checks all their packages.

`cloud-ssr` and `cloud-www` form a validation pair: a change to either selects
both server and browser checks. A browser change exercises all server packages.
The frontend job validates the npm lockfile, lint, TypeScript, Vitest coverage and
Vite production build. This validation pairing does not create a circular multicz
release dependency.

## Component releases and documentation publication

After successful validation on `main`, `release_components.py` reads the multicz
release plan for all registered components. Multicz updates the version files,
Zensical version table and component changelogs, then creates the signed
`chore(release): bump changed components` commit and component tags. Post-bump
hooks refresh the uv and npm lockfiles. The release script builds changed Go
commands, the frontend and documentation and checks the uv lockfile before
pushing the commit and all tags in a single atomic Git transaction. Concurrent
main updates or tag collisions reject the entire push; the script never
force-pushes main.

A newly introduced component without a tag and without a planned bump gets a
signed `0.0.0` seed tag. This also handles a root-only bootstrap. Components
already versioned above zero must have their atomic release tag. `cloud-docs`
depends on the implemented components so released version changes also update
and publish the version table. `cloud-ssr` depends on `cloud-www` so frontend
changes release the web server alongside the browser application.

The release PAT then creates a GitHub release with notes generated by
`multicz release-notes --tag`. The Pages job checks out the exact released commit,
builds the documentation and publishes it to
[the documentation site](https://goabonga.github.io/cloud/). Pages uses the
workflow's `GITHUB_TOKEN` with `pages: write` and `id-token: write`, independently
of the release PAT. Configure the repository's Pages publishing source as
**GitHub Actions**, with the `github-pages` environment permitting `main`.
See GitHub's [custom Pages workflow requirements](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).

With no planned bump, no untagged new component and no pending recovery, no new commit, tag, release or
deployment is created.
The signed release commit's follow-up CI validates it but skips the release job
so it cannot start a release loop. Runs on `main` are not cancelled by newer pushes.

If release creation fails after the atomic push, rerun the workflow: it reuses
the existing signed tag and commit and repairs a missing or draft GitHub release.
An already persisted release is reused even if its creation response was lost.
If Pages fails, rerun the failed job. A manual **Run workflow** on `main` can also
republish the current tagged documentation version without bumping it. Manual
runs on other branches cannot publish. A run whose validated commit has been
superseded leaves publishing to a workflow validating the newer `main`.

## Test the scripts

Every workflow writes its job summary through `ci_automation.py summary` in an
`always()` step. The signing workflows and Dependabot signal use the script from
the trusted default-branch checkout. Summaries append to existing output and
include the result, PR and signing identity when that context is available.

```console
uv tool install multicz --with multicz-go-deps-plugin
make check
# Run only the script checks:
make scripts-check
```

Tests use pytest functions and fixtures. The suite uses disposable repositories
and test GPG keys for signing,
fake GitHub clients for mutations and real SVG conversion for favicon validation.
It never changes repository settings, creates GitHub issues or merges a PR.
