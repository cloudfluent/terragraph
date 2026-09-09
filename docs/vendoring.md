# Vendoring third-party module sources

Vendoring lets you review and commit third-party module code with your blueprint. `terragraph vendor` fetches remote `node.source` addresses into local copies; later runs use those copies. Only Git sources are supported, not Terraform/OpenTofu Registry addresses. Sources starting with `./` or `../` are local; other addresses require vendoring.

## First fetch

Declare the Git source and select a revision with `ref`:

```hcl
node "vpc" {
  source = "git::https://github.com/terraform-aws-modules/terraform-aws-vpc.git?ref=v5.1.0"
}
```

The fetched module runs directly as an independent root module. It must have the provider and backend configuration needed for that use; terragraph does not generate a wrapper or provider/backend blocks. Supply inputs and environment settings through the [node configuration](blueprint.md).

```sh
terragraph vendor
terragraph validate
```

This creates `vendor/vpc/` and records its source in `vendor.yaml`:

```yaml
modules:
  - name: vpc
    source: git::https://github.com/terraform-aws-modules/terraform-aws-vpc.git?ref=v5.1.0
```

Commit both the fetched sources and the manifest. `.git` metadata is stripped from fetched copies, so they are not nested repositories. To fetch just one node, use `terragraph vendor --node vpc`.

`validate`, `graph`, `plan`, `apply`, and `destroy` do not fetch or update `node.source`; a missing copy tells you to run `vendor`. This does not make execution offline: Terraform/OpenTofu `init` can still download providers and remote modules referenced inside the fetched code.

An optional top-level block changes the default storage paths:

```hcl
vendor {
  directory     = "vendor"
  manifest_file = "vendor.yaml"
}
```

See [`examples/vendored`](../examples/vendored) for a runnable walkthrough.

## Updating sources

Change the node's `source` or `ref`, then run `terragraph vendor` again. A difference from the manifest triggers a fresh fetch without `--force`. Review the source diff and commit it with the updated blueprint and manifest. Changing the blueprint alone does not update the copy used by execution.

If the source string is unchanged, existing copies are left alone. Use `terragraph vendor --node vpc --force` to refresh a moving branch ref or reapply exclusions. A refresh replaces the copy rather than merging local edits, so preserve any changes you need first.

Manually populated copies without a manifest entry are also left alone unless forced, except for the legacy subdirectory upgrade described below.

## Excluding files

After the first fetch, add an `exclude` list to that node's manifest entry, then refresh it:

```yaml
modules:
  - name: vpc
    source: git::https://github.com/terraform-aws-modules/terraform-aws-vpc.git?ref=v5.1.0
    exclude:
      - "*.md"
      - "examples"
```

```sh
terragraph vendor --node vpc --force
```

Exclusions are per node and preserved on later vendor runs. Patterns without `/` match a basename at any depth; patterns with `/` match the full relative path. `**` is not a recursive glob. Exclude only files the module does not need to execute. `.git` is always removed.

## Groups and subdirectories

Remote nodes inside [local groups](groups.md) use qualified names in the root vendor directory and manifest. For example, `prod.inner.vpc` is fetched into `vendor/prod.inner.vpc/` and selected with `--node prod.inner.vpc`. Each instance receives its own copy; the group's source directory stays unchanged.

A source can select a repository subdirectory; replace the example repository below with your own:

```hcl
node "app" {
  source = "git::https://github.com/example/modules.git//modules/app?ref=v1.0.0"
}
```

The copy retains the whole repository package, while `.terragraph-source.json` identifies the selected execution directory. This preserves relative references such as `../common` without rewriting `.tf` files. Commit this metadata with the package; its filename is reserved and must not already exist at the upstream repository root. Exclusions are relative to the selected module, while `.git` is removed throughout the package. Exclusions cannot remove the package metadata.

Older flat subdirectory copies remain readable, but the next vendor run upgrades them to the package layout, even without `--force`. The state checks below apply before replacing them.

For older group layouts, an existing group-local copy is used until a root qualified copy exists. `group/vendor/<leaf>` takes priority over a custom group vendor directory when both exist. A normal vendor run preserves this fallback; a forced refresh, recorded source change, or subdirectory upgrade publishes a root copy after the state checks pass. The old group-local tree is retained. A present but broken root copy fails rather than falling back.

## Recovering a failed refresh

A failed fetch preserves that node's previous copy and manifest entry. A failed first fetch leaves no runnable copy. Other nodes may still finish and update their entries; a vendor run is not an all-or-nothing operation.

Before replacing an existing copy, vendoring checks local backend state, including `backend_config` overrides, workspace state, and backups. Existing state at a relative path or inside the vendor tree blocks the refresh. Migrate that state outside the tree and configure a stable absolute backend path before retrying; terragraph does not move state. Absolute state paths outside the tree are unaffected. Unknown local backend paths and differing Terraform/OpenTofu declarations also require review. These checks apply to source/ref changes, `--force`, and layout upgrades.

Invalid metadata or a selected directory that is missing or escapes the package is an error. `--force` cannot bypass it. Inspect the affected copy and its state paths, recover or migrate state outside it, and verify that removal is safe. Then remove only that copy and run `terragraph vendor --node <qualified-name>` again. Keep the metadata with the package during recovery; deleting only the marker can make terragraph mistake it for an older root-source copy.

## JSON failure results

`vendor --output json` keeps the existing array on success and for per-node failures. Each entry adds `diagnostics`; the existing `node`, `status`, and optional `error` remain. Per-node failures still exit nonzero without wrapping the array.

A global failure before results exist returns `{ "schema_version": 1, "diagnostics": [...] }`. A global failure after partial results returns `{ "schema_version": 1, "results": [...], "diagnostics": [...] }`. This changes the nonzero-exit shape for global failures that previously emitted an empty or partial array. Update consumers to accept these error shapes. No execution ID is created by vendoring. See [agent usage](agent-usage.md).
