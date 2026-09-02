## Summary

<!-- What does this change, and why? Link the issue it addresses, if any (Fixes #123). -->

## Checklist

- [ ] `make check` passes locally (fmt, lint, docs, build, test)
- [ ] Tests added or updated for the behavior change
- [ ] `docs/*.md` updated if this changes blueprint semantics, CLI flags, or an example's output (see [CONTRIBUTING.md](../CONTRIBUTING.md))

**PR title, not this body, becomes the commit message on `main` (squash-merge).** It's checked in CI and must follow [Conventional Commits](https://www.conventionalcommits.org/): `type: summary`, e.g. `fix: correct incremental-apply cache key`. See [CONTRIBUTING.md](../CONTRIBUTING.md) for the allowed types.
