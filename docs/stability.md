# Stability and deprecation

infrastructure follows [Semantic Versioning](https://semver.org/) per component.
Each component carries its own version and changelog, bumped from Conventional
Commits by [multicz](https://github.com/goabonga/multicz).

## Version bumps

| Commit type | Bump |
| --- | --- |
| `feat` | minor |
| `fix`, `perf`, `revert` | patch |
| `feat` announcing a deprecation | minor |
| removal of a public surface | major |
| `feat!` / `BREAKING CHANGE:` | major |

## Deprecation cadence

Public surfaces follow an `n + 2` cadence:

1. **Announce** in release `n + 1` with a `feat:` commit. The symbol keeps
   working and emits a deprecation notice.
2. **Remove** no earlier than release `n + 2`, in a major release with a
   `feat!:` commit and a `BREAKING CHANGE:` footer.

Deprecation notices do not make removal backward compatible. Every removal
of a public surface requires a major release, including security changes
that must bypass the deprecation cadence.
