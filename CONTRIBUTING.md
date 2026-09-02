# Contributing

## Issues

Bug reports and feature requests go through the templates GitHub shows when you click "New issue" (free-form issues are disabled, so there's always one of these). Fill in what's asked, especially `terragraph --version` and a minimal `blueprint.hcl` for bug reports: without a repro, a bug report usually just sits until someone can construct one.

## Making a change

External contributors: fork the repo and branch from `main`. Maintainers: branch directly on `origin`. Either way, branch names aren't enforced (a fork's branch lives outside this repo's server-side rules, so a name check would only ever apply unevenly); name it however's useful to you.

Before opening a PR: `make check` (fmt, lint, docs, build, test, the same thing CI runs) should pass locally. If the change touches blueprint semantics, a CLI flag, or an example's expected output, update the relevant `docs/*.md` or example `README.md` in the same PR; see the doc-sync rules in `CLAUDE.md`.

## PR title (this is the part that's enforced)

**The PR title becomes the commit message on `main`.** This repo merges everything via squash-merge, and GitHub uses the PR title as the squash commit's subject line, so the PR title is the one thing that's actually checked in CI for every contributor, fork or not:

```
type: short summary
```

`type` is one of `feat`, `fix`, `docs`, `refactor`, `test`, `perf`, `ci`, `build`, `chore`, `revert`. Examples: `fix: correct incremental-apply cache key`, `feat: add --output json to vendor`. The individual commits inside your branch don't need to follow this (they get squashed away); only the PR title does.

This isn't just style: `feat`/`fix`/etc. is what [release-please](https://github.com/googleapis/release-please) reads to decide the next version number and changelog entry (see below), so a wrongly-typed title produces a wrong changelog section, not just a CI failure.

## Merging

A PR needs: the PR-title check, all CI checks (`fmt, lint, docs, build, test` plus macOS/Windows build+test), and one approving review from a code owner, all green, before it can merge. See `docs/README.md` for what the CI job actually runs.

## Releases and versioning

You never pick a version number. [release-please](https://github.com/googleapis/release-please) watches `main`, and every `feat`/`fix`/breaking-change PR title merged there updates a standing "chore: release vX.Y.Z" pull request with the next [SemVer](https://semver.org) bump and changelog entry. A maintainer merges that PR when it's time to cut a release; that merge tags the release, and GoReleaser builds and attaches the Linux/macOS/Windows binaries automatically (`.github/workflows/release-please.yml`, `.goreleaser.yaml`). Nothing else to do on your end.

## Code of Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
