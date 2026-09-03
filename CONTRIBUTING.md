# Contributing

## Issues

Bug reports and feature requests go through the templates GitHub shows when you click "New issue" (free-form issues are disabled, so there's always one of these). Fill in what's asked, especially `terragraph --version` and a minimal `blueprint.hcl` for bug reports: without a repro, a bug report usually just sits until someone can construct one.

## Making a change

External contributors: fork the repo and branch from `main`. Maintainers: branch directly on `origin`. Either way, branch names aren't enforced (a fork's branch lives outside this repo's server-side rules, so a name check would only ever apply unevenly); name it however's useful to you.

Before opening a PR: `make check` (fmt, lint, docs, build, test, the same thing CI runs) should pass locally. If the change touches blueprint semantics, a CLI flag, or an example's expected output, update the relevant `docs/*.md` or example `README.md` in the same PR.

## What the PR body needs

Two things, and neither one is long.

**One sentence in your own words.** Somewhere in the body, in plain language, say what changed and why. A generated summary, a pasted agent transcript, or a filled-in checklist on its own doesn't count. One honest line does:

> I reviewed the full diff; this makes `vendor` skip a node whose source is already local, which is why a second run was re-fetching everything.

This isn't a rule about writing well. It's the only signal a reviewer has that the person opening the PR understands the change well enough to maintain it, and it costs nothing to produce if you do.

**What you actually ran.** `make check` passing is necessary, but it isn't evidence that the behavior works. Paste the commands you ran and what they printed: for a bug fix, the reproduction failing before the change and passing after it. Prefer a fenced code block to a screenshot of a terminal, since text can be searched, diffed, and quoted back to you in review. If you do need to attach an image, drag and drop it into the PR body rather than committing the file into the repo.

Using an AI agent to write the change is fine. Letting one open the PR without doing those two things is not, because you're the one who answers the review and maintains what lands.

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

The same tag also publishes the VS Code extension's six platform packages to the Marketplace (`.github/workflows/publish-vscode.yml`). The Marketplace is unreliable enough that this sometimes gets partway through; when it does, run that workflow manually from the Actions tab with the same tag. It skips whatever is already published and only retries what's missing, so a re-run is always safe. The VSIX files are attached to the GitHub Release either way.

## Code of Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
