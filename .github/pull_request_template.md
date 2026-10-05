## Description

<!-- Explain the problem and the resulting behavior. -->

## Validation

<!-- Describe the checks performed and any relevant limitations. -->

Run locally before pushing:

- [ ] `make check` passes.
- [ ] `make docs` passes.

## Related issues

<!-- Link related issues, for example: Closes #123. -->

## Checklist

- [ ] The pull request targets the intended base branch and references its issue.
- [ ] The change is focused and each commit is one logical change.
- [ ] Commit messages are Conventional Commits in English with at most one scope;
      a breaking change is marked with `!` and a `BREAKING CHANGE:` footer.
- [ ] Commits are signed with the contributor's configured identity.
- [ ] Documentation and configuration reference implemented features only.
- [ ] The pull request and its issue carry the labels that describe the change
      (`bug`, `enhancement`, `dependencies`, ...).

## Pipeline

<!-- Ticked by CI on each run; keep the ci-box markers. Text after a marker is replaced. -->

- [ ] audit <!-- ci-box: audit -->
  - [ ] zizmor <!-- ci-box: audit / zizmor -->
  - [ ] gitleaks <!-- ci-box: audit / gitleaks -->
- [ ] check commit signatures <!-- ci-box: check commit signatures -->
- [ ] detect changed components <!-- ci-box: detect changed components -->
- [ ] check license headers <!-- ci-box: check license headers -->
- [ ] validate scripts <!-- ci-box: validate scripts -->
  - [ ] pytest <!-- ci-box: validate scripts / pytest -->
  - [ ] ruff <!-- ci-box: validate scripts / ruff -->
  - [ ] bandit <!-- ci-box: validate scripts / bandit -->
  - [ ] osv-scanner <!-- ci-box: validate scripts / osv-scanner -->
- [ ] validate go <!-- ci-box: validate go -->
  - [ ] go test <!-- ci-box: validate go / go test -->
  - [ ] gosec <!-- ci-box: validate go / gosec -->
  - [ ] go-mod-verify <!-- ci-box: validate go / go-mod-verify -->
  - [ ] govulncheck <!-- ci-box: validate go / govulncheck -->
  - [ ] golangci-lint <!-- ci-box: validate go / golangci-lint -->
  - [ ] osv-scanner <!-- ci-box: validate go / osv-scanner -->
- [ ] validate documentation <!-- ci-box: validate documentation -->
