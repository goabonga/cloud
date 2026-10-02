## Description

<!-- Describe what this PR does and why. -->

## Type

<!-- Check the one that applies: -->

- [ ] `feat` - New feature
- [ ] `fix` - Bug fix
- [ ] `perf` - Performance improvement
- [ ] `docs` - Documentation
- [ ] `refactor` - Code refactoring
- [ ] `test` - Adding or updating tests
- [ ] `chore` - Maintenance
- [ ] `ci` - CI / release pipeline

## Scope

<!-- Which component(s) does this PR touch? -->

- [ ] `infra-api`
- [ ] `infra-controller-manager`
- [ ] `infra-agent`
- [ ] `infra` (cli)
- [ ] `terraform-provider-infra`
- [ ] `infra-idp`
- [ ] `infra-exporter`
- [ ] `infra-container-init`
- [ ] core / repo-level (internal, CI, docs)

## Changes

<!-- List the main changes introduced by this PR: -->

-

## Related Issues

<!-- Link related issues: Closes #123, Fixes #456 -->

## Checklist

- [ ] Commits follow [Conventional Commits](https://www.conventionalcommits.org/) and target the right component via the scope (`feat(api): ...`)
- [ ] Branch is up to date with `main`
- [ ] `go build ./...` succeeds
- [ ] `go vet ./...` is clean
- [ ] `golangci-lint run` is clean
- [ ] `go test -race ./...` passes
- [ ] `uv tool run multicz validate --strict` passes
- [ ] SPDX license headers are present (`make license-check`)
- [ ] No `Co-Authored-By` trailer in commit messages
