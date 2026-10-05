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
| `AUTO_MERGE_PAT` | Check authorization, push signed commits, fast-forward `main`, label Dependabot pull requests | Contents: write; Pull requests: read; Workflows: write; Issues: write |
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
`GITHUB_TOKEN`, with `checks: read` and `statuses: read`, and also comments on the
pull request and removes its `auto-merge` label with it (`pull-requests: write`).
Its dedicated PAT handles PR authorization and Git pushes. Select only the PAT
permissions in the table; the workflow token supplies the CI and PR permissions.
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

To apply only the branch protection, which needs no secrets and can run on
a fresh repository, use `protect`. It reconciles the `main-protection` ruleset —
covering `main` and `develop` — and leaves settings, labels, variables and
secrets untouched:

```console
python3 scripts/init_github.py protect --repo goabonga/cloud
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

`init` always applies the `main-protection` ruleset first. If required secrets are
missing, it then exits with status 1 without changing the other configuration and lists the
commands needed to supply them. Create
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

## Dependabot updates

Dependabot opens grouped pull requests every Monday, one per ecosystem and, for `uv`,
one for runtime and one for development dependencies:

| Ecosystem | Directory | Rewritten subject |
| --- | --- | --- |
| `github-actions` | `/` | `ci: …`, which patches every component owning the workflows |
| `gomod` | `/` | `fix(deps): …`; modules are linked into the shipped binaries |
| `uv` | `/scripts` | `fix(deps): …` for runtime dependencies, which release `cloud-scripts`; `chore(deps): …` when only development dependencies change |

The rewrite workflow refuses a commit whose files span ecosystems or fall outside
them, leaving the pull request unchanged.

### Automatic merge of Dependabot updates

`dependabot-auto-merge` runs trusted code from `main` whenever `ci` succeeds on a
Dependabot pull request. When CI passed on the current head and every commit is
rewritten and signed, it applies the `auto-merge` label with `AUTO_MERGE_PAT`, so
the pull request joins the [merge queue](#signed-automatic-merge). A label added
with the workflow token would not start `auto-merge-signed`, because GitHub does not
chain workflows triggered by that token.

Major updates, recognized by the `update-type: version-update:semver-major` metadata
Dependabot leaves in the commit message, are not labelled. The pull request gets a
single comment explaining that it waits for review: a maintainer with write access
merges it by adding the `auto-merge` label, which queues it like any other pull
request.

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

Each validation job runs only for what the change touches. Jobs are:

| Job | Runs when | Checks |
| --- | --- | --- |
| `audit` | always | Plumber compliance |
| `check commit signatures` | every pull request and push, after `audit` | each PR or pushed commit carries a signature verified by GitHub; a failure stops the pipeline |
| `detect changed components` | always, after `audit` and, on pull requests and pushes, after a successful signature check | multicz configuration, changed components |
| `check license headers` | at least one component changed, after detection | SPDX headers |
| `validate scripts` | `cloud-scripts` changed | byte-compilation, and the tests the change affects |
| `validate go` | a registered Go component changed, through its own paths or its imports | formatting, vet, race tests, build and gosec |
| `validate documentation` | `cloud-docs` changed (`docs/**`, `zensical.toml`, `assets/cloud.svg`) | generated branding, documentation build |

Detection runs `multicz changed` and nothing else: a component changed when it has
commits since its latest tag, exactly as in the reference pipeline. A commit that changes
no component, such as a `chore`, therefore runs no validation, no release and no
documentation publication. Only `audit` and detection itself always run; on a pull
request or a push, an unsigned commit stops the pipeline before detection, so nothing is
validated or released.
Scripts and their tests are selected from the diff against the PR base or the previous
push, which only narrows which test files run.

Every job selects its work from the `changed` output of `multicz changed`, which is the
only source of component changes. Its inputs are:

- each component's `paths` globs;
- the `go-deps` plugin, which adds the files a Go component imports (its command
  packages are registered under `plugins.go-deps.packages`). A change in
  `internal/transport` therefore selects `cloud` and `cloud-svc`;
- `overlap_policy = "all"` for shared module or workflow changes.

`cloud-docs` declares `depends_on` on `cloud`, `cloud-svc` and `cloud-scripts`: a change
to any of them also selects the documentation, and its release patches `cloud-docs`, so
the published version table is rebuilt with the new version.

Script validation compiles every script and runs the test files that import a changed
module. Changes to `scripts/ci_automation.py`, `scripts/pyproject.toml`,
`scripts/uv.lock` or `pytest.ini` run the whole suite, as does any script without a
test that imports it.

Go validation is a single job rather than a matrix. Matrix job names depend on the
components that changed, so they cannot be required checks.

### Failure reports on pull requests

On a pull request, the `report job failures` job runs last, whatever the other jobs
concluded. It keeps one comment per job that failed, marked with a hidden
`<!-- ci-job: <name> -->` line: the failed step, the error lines of the job log and a
link to it. A later run edits that same comment instead of adding another, and marks
it resolved once the job passes again; jobs that never failed get no comment. It runs
trusted code from `main` with the workflow token (`actions: read`,
`pull-requests: write`) and is not a required check.

### Required checks

The branch ruleset requires the seven job names above. A job skipped because its
component did not change counts as passing. A failed or cancelled job blocks the
merge, and a skipped job is still allowed only if its `if` condition was false.

The `release-bump` job runs only when some component changed and no validation job failed
or was cancelled (`!failure() && !cancelled()`), so a failing check stops the release.

## Signed automatic merge

A maintainer with write access merges a pull request into `main` by applying the
`auto-merge` label. The `auto-merge-signed` workflow then runs only trusted code
from `main`:

1. It checks the label, the pull request head and the labeller's access.
2. It waits for its turn in the merge queue. Pull requests are served in the order
   of their latest `auto-merge` label, one at a time, and only once `multicz plan`
   on `main` is empty, so a release left by the previous merge is published first.
   A merge that bumps nothing lets the next pull request start right away.
3. It waits for the pull request checks, rebases onto `main`, signs every commit
   with the maintainer key and pushes the branch with an explicit lease.
4. It waits for the checks of the signed commits, then fast-forwards `main`.

Any failure comments on the pull request and removes the label, which also takes
the pull request out of the queue; re-apply the label to retry. Removing the label
by hand skips a pull request that is blocking the queue.

## Component releases and documentation publication

Releases are driven entirely by multicz. Nothing in the repository reads version
files or computes tags.

1. `release-bump` runs only on `main`, after every validation job succeeded or was
   skipped, and never on a `chore(release):` commit. It runs `multicz plan`. If the
   plan is empty, the job stops: no commit, no tag, no release. Otherwise it runs
   `multicz bump --commit --tag --sign --push`, which writes the version files and
   changelogs, creates one signed tag per bumped component, and pushes them. The bump
   commit message comes from `release_commit_message` in `multicz.toml`: the subject
   `chore(release): bump changed components`, the list of bumps in the body, and a
   `[skip ci]` footer, so pushing it starts no new pipeline.
2. `release` runs one job per bumped tag. It checks out the tag, builds the assets
   with steps in the job, and creates the GitHub release with
   `multicz release-notes --tag` as notes.
3. `pages` builds and deploys the documentation from the bump commit, which contains the
   refreshed version table.

Assets are linux `amd64` and `arm64` binaries for `cloud` and `cloud-svc`, a
`tar.gz` of the built site for `cloud-docs`, and a `-checksums.txt` file with SHA-256
digests. `cloud-scripts` ships no asset.

A component is first released by its first `feat` or `fix` commit, as `0.1.0` or
`0.0.1` respectively. A `chore` commit never bumps a component.

The release PAT pushes the bump commit and creates the releases. The Pages job uses the
workflow's `GITHUB_TOKEN` with `pages: write` and `id-token: write`, independently of the
release PAT. Configure the repository's Pages publishing source as **GitHub Actions**,
with the `github-pages` environment permitting `main`. See GitHub's
[custom Pages workflow requirements](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).

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
