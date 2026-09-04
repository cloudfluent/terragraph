# Contracts

Contracts turn graph edges into reviewed, two-sided promises: a producer
declares what one of its outputs guarantees; a consumer declares what one of
its inputs requires. Contracts are advisory by default: violations surface as
warnings in `terragraph validate`, and the blueprint's `contracts { mode =
"enforce" }` block (see [Modes](#modes)) is the only way to make them block;
it is never enabled silently.

## Where contracts live

`producer` and `consumer` are ordinary top-level blueprint blocks. They parse
from any `.hcl` file a blueprint parses: a single-file blueprint, every file
of a directory blueprint (which merge like every other block kind), and a
group body — so a group's own `group.hcl` carries the contracts for its
internal modules, resolved against the group file's own directory. There is
no reserved filename: `contracts.hcl` is a convention, not a mechanism — a
file by that name is just another blueprint file whose blocks merge with the
rest.

## Grammar

```hcl
producer "./modules/vpc" {
  output "vpc_id" {
    type      = "string"        # Terraform type constraint syntax
    nullable  = false           # this output is never null
    sensitive = false           # not a secret
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

The block label is the module source, spelled exactly as a node's `source`:
a relative path (`./modules/vpc`, `../shared/vpc`) or a remote module source
(`github.com/org/repo//modules/vpc`). Absolute paths are rejected at parse
time.

A port carries at most three attributes — `type` (a Terraform type
constraint, parse-checked where the file and port are known), `nullable`, and
`sensitive` — and the grammar carries nothing `validate` does not check:
there is no predicate syntax and no `stability`.

Attribute absence is the *lenient* claim: an omitted `nullable` on a producer
means "may be null" (a weak promise), an omitted `nullable` on a consumer
means "accepts null", and an omitted `sensitive` claims nothing. Checks only
fire on explicit strictness the other side does not meet.

## Keying: one contract per source, not per node

Contracts are keyed by module source, not by node name. A local scope keys by
the directory it resolves to against the declaring file's directory — the
same base a node source in that file resolves against. A remote scope keys
by the declared source string itself, because a remote node's vendored
directory is per-instance (`vendor/<node-name>`) while the contract belongs
to the source everything was vendored from. Every node sharing a source
shares its contract: a group instantiated twice, one module reused through
`backend_config`, or two vendored instances of the same remote module inherit
the same contract everywhere they appear.

## Facts, and who declares them

Terraform modules already declare most contract facts in their own `variable`
and `output` blocks. `validate` reconciles every explicit contract claim
against the module's schema:

| Fact | Declared by the module | Reconciled |
|---|---|---|
| Consumer `type` | `variable` type constraint | yes — C007 |
| Consumer `sensitive` | `variable` `sensitive` | yes — C008 |
| Producer `sensitive` | `output` `sensitive` | yes — C009 |
| Producer `type` | nothing — a root-module output cannot declare a type | no — the contract's reason to exist |

Producer output type is the one fact no `.tf` file can declare, which is
exactly why the producer side of a contract exists: the contract is the only
place that promise can be written down, and the compatibility checks (C003)
are the only thing that reviews it.

## Error classes and codes

`terragraph validate` reports three classes. C001/C002/C006 are existence and
scope: a promise about a port the module never declared, or a scope nothing
instantiates, is wrong whether or not anything consumes it yet. C003–C005 are
side-vs-side compatibility, checked for every data edge whose endpoints both
carry contracts on that port — producer and consumer each keep their word and
still cannot be wired together. C007–C009 are contract-vs-module
contradiction: the module's `variable` and `output` blocks are the
declaration of record, and a contract claiming a different type or
sensitivity is simply wrong about the module it describes — fix the contract,
not the wiring. Reconciliation fires only on explicit claims (an absent
attribute is no claim, and a variable with no type constraint has nothing to
contradict), in both directions. Uncontracted endpoints check nothing — that
is the migration path.

| Code | Severity | Fires when |
|---|---|---|
| C001 | warning | producer contract names an output the module does not declare |
| C002 | warning | consumer contract names an input variable the module does not declare |
| C003 | warning | producer type is not convertible to the consumer's required type (cty `ConvertibleTo`) |
| C004 | warning | consumer requires non-null (`nullable = false`) but the producer allows null |
| C005 | warning | producer is `sensitive = true` but the consumer does not accept sensitive values |
| C006 | warning | contract scope matches no node in the graph (stale path after a move or rename) |
| C007 | warning | consumer's claimed `type` contradicts the module's declared variable type constraint |
| C008 | warning | consumer's explicit `sensitive` claim contradicts the module's declared variable sensitivity |
| C009 | warning | producer's explicit `sensitive` claim contradicts the module's declared output sensitivity |

Every code is a warning under the default mode; the mode block below is the
one severity dial, and it moves all of them together — there is no per-code
severity.

### Modes

`contracts { mode = "..." }` in the blueprint is reviewed configuration and
the only severity dial: `warn` (the default when the block is absent) reports
every C001–C009 as a warning; `enforce` escalates them to errors, which
blocks `validate`, `plan`, `apply`, and `destroy` the same way structural
errors already do. An upgrade never selects a stricter mode on its own.

## Contract identity

A contract set's identity is `sha256` hex over its canonical JSON: one entry
per port covering exactly the checked claims — scope as written, role, port
name, `type`, `nullable`, `sensitive` — with every entry sorted by (scope,
role, name). Editing a claim changes the digest; reordering or re-splitting
blocks across files never does. Two instances of one source report the same
digest because they share one contract record.

## Deferred

- The evidence layer (`terragraph.lock`, `observe`, `propose`) — removed per review; runtime evidence may return once the declarative layer settles.
- Predicate syntax (`assert` blocks) — no check evaluates it today.
- LSP/VS Code support for contract blocks — returns once the grammar settles.
