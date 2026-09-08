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

A fetch is prepared and pruned in a temporary directory before replacing the existing vendored copy. A failed fetch leaves the previous copy and manifest intact; a failed first fetch leaves no runnable node directory. Existing manually populated directories without a manifest entry are still left alone unless `--force` is requested.

For a git source selecting `//modules/app`, the vendor directory retains the **whole repository package** and `.terragraph-source.json` records `modules/app` as its execution directory. This keeps paths such as `../common` inside the original package; terragraph never rewrites `.tf` files. The metadata is engine-managed and committed with the package. An upstream repository containing a root file with this reserved name is rejected. Invalid metadata or a subdirectory escaping the package is an error, not a fallback to another module.

Older versions stored only the selected subdirectory. The next `terragraph vendor` refreshes these legacy subdirectory copies into the package layout even without `--force`; a failed refresh preserves the old copy. Existing root-source directories, including manually populated ones without a manifest, keep their previous behavior. Until refreshed, an existing flat copy is still read as before. Per-node `exclude` patterns remain relative to the selected module, while `.git` is stripped throughout the retained package. The package-layout metadata is never removed by an exclusion pattern.

Before changing a legacy copy's execution directory, vendoring checks its implicit or declared local backend state paths, node `backend_config` overrides, and local workspace state, including backups. Existing state at a relative path or inside the old vendor tree stops the upgrade and leaves the tree and manifest unchanged. Move or migrate that state outside the vendor tree and configure a stable absolute backend path before retrying; terragraph does not move state automatically. Because vendoring does not choose an execution runtime, differing Terraform and OpenTofu declarations require review before changing directories. An unknown local backend path also requires review instead of an assumed-safe upgrade. Absolute state paths outside the tree are unaffected.
