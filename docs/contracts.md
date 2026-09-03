# Contracts

Contracts turn graph edges into reviewed, two-sided promises: a producer
declares what one of its outputs guarantees; a consumer declares what one of
its inputs requires. Contracts are advisory in this phase: violations surface
as warnings in `terragraph validate`. Enforcement modes arrive later and are
never enabled silently.

## Where contracts live

`contracts.hcl` sits next to the blueprint file (same directory-merge rule as
blueprints: if `--blueprint` names a directory, every `contracts*.hcl` file
directly inside it is merged). Graphs without a `contracts.hcl` behave exactly
as before.

Contracts are **keyed by module source directory**, not by node name. Every
node that shares a source directory shares its contract — so a group
instantiated twice, or one module reused through `backend_config`, inherits
the same contract everywhere it appears. This matches how terragraph already
sees the world: by the time the graph is built, every edge is leaf-to-leaf
and module schemas are cached per directory.

## Grammar

```hcl
producer "./modules/vpc" {
  output "vpc_id" {
    type      = "string"        # Terraform type constraint syntax
    nullable  = false           # this output is never null
    sensitive = false           # not a secret
    stability = "stable"        # "stable" (default) | "volatile"
    assert {
      nonempty = true           # value must not be "" when a string
      pattern  = "^vpc-"        # value must match this regex
    }
  }
}

consumer "./modules/app" {
  input "vpc_id" {
    type      = "string"
    nullable  = false           # this input must never receive null
    sensitive = false           # refuses values marked sensitive upstream
  }
}
```

Attribute defaults are the *lenient* claim: an omitted `nullable` on a
producer means "may be null" (a weak promise), and an omitted `nullable` on a
consumer means "accepts null". Violations only fire on explicit strictness
that the other side does not meet.

`assert` predicates are declared now and evaluated later, by the observe
command (a follow-up phase) that has actual values to test. `validate` never
guesses at values. The phase-1 predicate set is closed — `nonempty`,
`pattern`, `min_length` (strings/lists), `one_of` — so digests stay stable;
new predicates are additions, never syntax changes.

## Contract identity

A contract's identity is `sha256` over its canonical JSON: scope as written
(`./modules/vpc`), role, port name, and every set attribute, with ports
sorted by name. Renaming a path changes identity even when content is
identical; reordering blocks never does. Two instances of one source report
the same digest because they share one contract record.

## Compatibility rules and error codes

Checked by `terragraph validate` for every data edge whose endpoints both
carry contracts, plus a schema sanity check for every declared contract:

| Code | Severity | Fires when |
|---|---|---|
| C001 | warning | producer contract names an output the module does not declare |
| C002 | warning | consumer contract names an input variable the module does not declare |
| C003 | warning | producer type is not convertible to the consumer's required type (cty `ConvertibleTo`) |
| C004 | warning | consumer requires non-null (`nullable = false`) but the producer allows null |
| C005 | warning | producer is `sensitive = true` but the consumer does not accept sensitive values |
| C006 | warning | contract scope matches no node in the graph (stale path after a move or rename) |

All contract problems are warnings in this phase. A later phase adds
blueprint-owned modes (`legacy`/`observe`/`warn`/`enforce`); an upgrade never
selects a stricter mode on its own.

## Non-goals for this phase

- No terragraph-written files. The generated evidence file (`terragraph.lock`)
  and the observe/proposal commands are a separate, explicitly-approved change
  — terragraph currently writes exactly two kinds of file (ephemeral tfvars,
  vendor manifest) and this phase adds none.
- No group-local `contracts.hcl` inside group source directories; the root
  file can already reach group-internal modules by relative path.
- No cross-check of contract `sensitive` against the module's own
  `sensitive = true` output flag; the contract is intent, the module is
  declaration, and reconciling them is observe's job.
- No arbitrary assertion expressions. Predicate set only (see above).
