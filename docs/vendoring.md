# Vendoring third-party module sources

`node.source` can point at a remote git address instead of a local path, the same rule Terraform's own `module.source` uses: anything not starting with `./` or `../` is remote:

```hcl
node "vpc" {
  source = "git::https://github.com/terraform-aws-modules/terraform-aws-vpc.git?ref=v5.1.0"
}
```

Nothing fetches it live. `terragraph vendor [--node NAME] [--force]` is a separate, explicit step that downloads it once into `vendor/<name>/`, meant to be **committed**, so a version bump shows the actual `.tf` content change in `git diff`, not just a one-line ref bump the way a live `module { source = "...", version = "..." }` wrapper would. `validate`/`graph`/`plan`/`apply`/`destroy` never fetch anything; an unvendored remote node fails clearly, telling you to run `terragraph vendor` first.

Each vendored node gets an entry in `vendor.yaml` (also committed):

```yaml
modules:
  - name: vpc
    source: git::https://github.com/terraform-aws-modules/terraform-aws-vpc.git?ref=v5.1.0
```

That's deliberately all that's tracked: the `ref` in `source` is already the version pin, so there's no separate resolved-commit/fetched-at/content-hash bookkeeping to keep in sync with it. Instead, `vendor.yaml` is compared against the blueprint's *current* `node.source` on every vendor run: if they differ (a `ref` bump, or any other source change), that node is re-fetched automatically; no `--force` needed, the same way `git pull` notices you're behind without being told which commit to compare against. `--force` is only for re-fetching a node whose `source` *hasn't* changed (e.g. to pick up new commits on a moving branch ref). `.git` is always stripped from the fetch, so it never ends up committed as a nested repo.

**`exclude` is per node, not project-wide**, because different upstream repos need different files pruned. It's the one field in the manifest entry the tool never computes: it starts empty, and a vendor run only ever reads it (to prune with) and writes it back unchanged. To prune something from one specific vendored module: vendor it once, hand-edit its `exclude` list in `vendor.yaml` (patterns with `/` anchor to the full path; patterns without match the basename at any depth, e.g. `*.md`), then `terragraph vendor --node <name> --force` to re-fetch with it applied.

Project-wide layout is configurable via an optional `vendor { }` block:

```hcl
vendor {
  directory     = "vendor"       # default
  manifest_file = "vendor.yaml"  # default
}
```

Only git sources are supported today; the fetch mechanism is an interface so a Terraform/OpenTofu Registry backend can be added later without changing anything above it.

See it end to end in [`examples/vendored`](../examples/vendored).
