#!/usr/bin/env bash

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Rewrite the current HEAD commit with a Conventional Commits prefix inferred
# from the files it touches, then amend with --reset-author and a GPG signature
# so the commit is attributed to and signed by the maintainer's identity
# (configured by the caller workflow).
#
# Routing rules (first-match wins):
#
#   .github/workflows/*.yml   -> ci:
#   go.mod / go.sum           -> fix(deps):  if a binary links something new
#                                chore(deps): otherwise
#   www/package*.json         -> fix(deps):  if the bump reaches the bundle
#                                chore(deps): otherwise
#   anything else             -> chore(deps):
#
# fix(deps) is what makes multicz release: it patch-bumps the components that
# own the touched files (go.mod / go.sum -> every Go binary, www/** ->
# infra-spa), while chore(deps) releases nothing. The test is therefore "does
# a shipped artifact change", not "is this a dev dependency":
#
#   - Go has no dev dependencies, so compare what ./cmd/... actually links
#     (`go list -deps`, test-only packages excluded) at HEAD~1 and HEAD. A
#     change to the go or toolchain directive always ships: it changes the
#     compiler and the standard library.
#   - npm: runtime dependencies ship, and so does vite with its plugins,
#     which are devDependencies that decide the emitted assets (keep in step
#     with the www-bundler group in .github/dependabot.yml). The rest of the
#     direct devDependencies is tooling. An indirect dependency cannot be
#     placed on either side from the Dependabot metadata alone, so it is
#     counted as shipped: an extra patch release is cheaper than a security
#     fix that never goes out.
#
# Designed to be invoked by `git rebase --exec` against each commit of a
# Dependabot pull request, from the repository root. `go list` resolves the
# module graph only - it never builds or runs module code - and -mod=readonly
# keeps it from rewriting go.mod. Stand-alone use is fine too.

set -euo pipefail

changed=$(git show --name-only --pretty='' HEAD)
original=$(git log -1 --pretty=%B HEAD)

# Every non-main module a ./cmd binary links, with its version (and
# replacement, if any), one per line. Run from the module root.
linked_modules() {
    GOFLAGS=-mod=readonly go list -deps \
        -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{with .Replace}} => {{.Path}}@{{.Version}}{{end}}{{end}}{{end}}' \
        ./cmd/... | sort -u
}

go_bump_ships() {
    if git diff HEAD~1 HEAD -- go.mod | grep -qE '^[-+](go|toolchain) '; then
        return 0
    fi
    # Global rather than local: the EXIT trap outlives this function.
    base=$(mktemp -d)
    git worktree add --quiet --detach "$base" HEAD~1
    trap 'git worktree remove --force "$base"' EXIT
    local before after
    before=$(cd "$base" && linked_modules)
    after=$(linked_modules)
    [ "$before" != "$after" ]
}

# Reads the `updated-dependencies:` metadata Dependabot writes into the commit
# body and succeeds as soon as one entry reaches the bundle.
npm_bump_ships() {
    printf '%s\n' "$original" | awk '
        /^- dependency-name:/ { name = $3 }
        /^  dependency-type:/ {
            type = $2
            if (type != "direct:development") shipped = 1
            else if (name == "vite" || name ~ /^@vitejs\//) shipped = 1
            seen = 1
        }
        END { exit (shipped || !seen) ? 0 : 1 }
    '
}

case "$changed" in
    *.github/workflows/*)
        prefix="ci"
        ;;
    *go.mod* | *go.sum*)
        if go_bump_ships; then prefix="fix(deps)"; else prefix="chore(deps)"; fi
        ;;
    *www/package*.json*)
        if npm_bump_ships; then prefix="fix(deps)"; else prefix="chore(deps)"; fi
        ;;
    *)
        prefix="chore(deps)"
        ;;
esac

# Strip any existing Conventional-style prefix from the subject and lowercase
# the leading "Bump" that Dependabot uses by default.
subject=$(printf '%s' "$original" \
    | head -n 1 \
    | sed -E 's/^[a-z-]+(\([^)]+\))?:[[:space:]]*//' \
    | sed 's/^[Bb]ump /bump /')

# Preserve the body but drop any `Co-Authored-By:` trailer - this repo enforces
# a strict no-co-author policy.
body=$(printf '%s' "$original" \
    | tail -n +2 \
    | sed '/^Co-Authored-By:/d')

if [ -n "$body" ]; then
    new_msg=$(printf '%s: %s\n\n%s' "$prefix" "$subject" "$body")
else
    new_msg=$(printf '%s: %s' "$prefix" "$subject")
fi

git commit --amend --reset-author -m "$new_msg" --quiet
