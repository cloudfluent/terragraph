## Summary

<!-- What does this change, and why? Link the issue it addresses, if any (Fixes #123).

     At least one sentence here has to be written by you, in plain language. A generated
     summary or pasted agent output on its own doesn't satisfy this. One honest line that
     shows you understand the change is enough. -->

## Verification

<!-- The commands you actually ran and what they printed. For a bug fix, show the
     reproduction failing before the change and passing after it.

     Prefer a fenced code block to a screenshot of a terminal: text can be searched,
     diffed, and quoted back to you in review. If you do attach an image, drag and drop
     it into this body rather than committing the file into the repo. -->

- [ ] `make check` passes locally (fmt, lint, docs, build, test)

## Checklist

- [ ] Tests added or updated for the behavior change
- [ ] `docs/*.md` updated if this changes blueprint semantics, CLI flags, or an example's output (see [CONTRIBUTING.md](../CONTRIBUTING.md))

**PR title, not this body, becomes the commit message on `main` (squash-merge).** It's checked in CI and must follow [Conventional Commits](https://www.conventionalcommits.org/): `type: summary`, e.g. `fix: correct incremental-apply cache key`. See [CONTRIBUTING.md](../CONTRIBUTING.md) for the allowed types.
