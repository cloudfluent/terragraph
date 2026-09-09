# Contracts

Contracts help review whether connected modules agree on the values they
exchange. A producer declares an output's promised type, nullability, or
sensitivity; a consumer declares what its input expects. They are useful when
reusing modules or reviewing an interface change before execution.

`validate` and `graph` check declarations without running Terraform or reading
state. During execution, terragraph also checks explicit contracts against
actual values. In `enforce` mode a known producer violation blocks its apply;
computed outputs are checked after that producer applies, before downstream
nodes receive them. Consumer requirements are checked against the effective
module inputs from the exact saved plan before applying it.

## Grammar

For modules that expose an output and input named `vpc_id`, add contracts to
the blueprint alongside their data edge:

```hcl
node "vpc" { source = "./modules/vpc" }
node "app" { source = "./modules/app" }

edge {
  from = node.vpc.output.vpc_id
  to   = node.app.input.vpc_id
}

producer "./modules/vpc" {
  output "vpc_id" {
    type      = string
    nullable  = false  # promises a non-null output
    sensitive = false
  }
}

consumer "./modules/app" {
  input "vpc_id" {
    type      = string
    nullable  = false  # requires the producer's non-null promise
    sensitive = false
  }
}
```

The module's input must accept `string`, and its input and output sensitivity
declarations must agree with the explicit `sensitive = false` claims above.
See the [contracts example](../examples/contracts) for a complete blueprint
and modules.

Each port accepts three optional attributes. `type` accepts native Terraform
type constraints, such as `string`, `list(string)`, or
`object({ id = string, tags = optional(map(string)) })`. Existing quoted forms
such as `"list(string)"` remain supported and have the same identity. Attribute
order and whitespace do not affect identity; optionality does. Defaults in
`optional(type, default)` belong in module variables and are rejected in contracts.

| Omitted attribute | Producer | Consumer |
|---|---|---|
| `type` | No additional type promise | No additional type requirement; the module variable type still applies |
| `nullable` | May return null | Accepts null |
| `sensitive` | No sensitivity claim | Does not declare acceptance of sensitive values |

If the producer contract declares `sensitive = true`, the consumer contract
must explicitly declare `sensitive = true` too. The corresponding module
output and variable must also declare that sensitivity.

## Modes

Start by checking the blueprint:

```sh
terragraph validate
```

The default mode, `warn`, reports contract findings without failing the
command. After reviewing both declaration and runtime findings, make violations block commands by adding
this top-level block:

```hcl
contracts {
  mode = "enforce"
}
```

`enforce` makes every C001–C009 finding an error for `validate`, `graph`,
`plan`, `apply`, and `destroy`. Set `mode = "warn"` or remove the mode block
to return to warnings. There is no per-code severity setting. Invalid syntax
and conflicting contract declarations remain errors in either mode. Runtime
violations use C010. Missing evidence uses C011: it blocks a required consumer
check before apply and a required producer check after apply. An output unknown
at plan time is deferred and does not by itself block that producer's apply.
`warn` reports both without bypassing native input errors or missing edge values.

## Where contracts live

`producer` and `consumer` are top-level blueprint blocks or blocks inside a
`group` body. In a directory blueprint, they can live in any parsed `.hcl`
file. `contracts.hcl` is only a naming convention: when loading a single
blueprint file, sibling files are not loaded automatically.

The label is a module source: a relative path such as `./modules/vpc` or
`../shared/vpc`, or a remote source such as
`github.com/org/repo//modules/vpc`. Absolute paths are rejected.

## Keying: one contract per source, not per node

All nodes using the same source share its contract. Local paths resolve
against the declaring file's directory, including contracts inside a group.
Remote sources must match the node's declared source string, rather than its
per-node vendored path. Every actual module copy is checked, including copies
used by different group instances.

There is no per-node override. If a group and its enclosing blueprint declare
the same source, role, and port, identical claims are shared; different claims
are an error. Within one blueprint's files, declaring the same source, role,
and port twice is an error even when the claims agree.

## Facts, and who declares them

Contracts check that named ports exist. When both ends of a data edge have
contracts for those ports, terragraph also compares their type, nullability,
and sensitivity claims. A port without a contract skips this comparison, so
contracts can be adopted one connection at a time. Type compatibility is
checked only when both contracts specify a type; a possible conversion does
not guarantee that every actual value will convert successfully.

Explicit claims are also checked against the module's declarations:

| Claim | Checked against the module |
|---|---|
| Consumer `type` | The variable's type constraint, if declared (C007) |
| Consumer `sensitive` | The variable's sensitivity, in either direction (C008) |
| Producer `sensitive` | The output's sensitivity, in either direction (C009) |
| Producer `type` | Actual planned or applied output structure and primitive types |
| `nullable` | Top-level actual output or effective module input; module defaults can replace a null input |

A consumer contract can be narrower than the module's input type. For example,
a module variable declared as `map(any)` can have this consumer contract:

```hcl
consumer "./modules/app" {
  input "tags" { type = "map(string)" }
}
```

C007 accepts that narrowing, but reports `string` against a `number` variable:
not every string can be converted to a number. The narrower contract is also enforced against the effective module input during execution.

## Error classes and codes

C001–C010 are warnings in `warn` mode and errors in `enforce` mode. C011 is deferred evidence and follows the execution rules above.

| Code | Reported when |
|---|---|
| C001 | Producer contract names an output the module does not declare |
| C002 | Consumer contract names an input variable the module does not declare |
| C003 | Producer type cannot be converted to the consumer's required type |
| C004 | Consumer requires non-null but the producer does not promise it, and the module has no non-null default with `nullable = false` |
| C005 | Producer declares `sensitive = true` but the consumer does not |
| C006 | Contract source matches no node in the graph; update the source or remove the contract |
| C007 | Consumer's claimed type cannot be safely converted to the module variable's declared type |
| C008 | Consumer's explicit sensitivity differs from the module variable's declaration |
| C009 | Producer's explicit sensitivity differs from the module output's declaration |
| C010 | An actual value violates an explicit contract condition |
| C011 | Required runtime evidence is unavailable or not yet known |

## Runtime value rules

- Producer checks preserve primitive types: `123` does not satisfy `string`.
- Objects may have extra attributes. Optional attributes may be absent. Nested
  nulls are allowed; `nullable = false` applies only to the top-level port.
- Lists accept tuples whose elements meet the element constraint. Tuples require
  the declared length and position types. Sets require actual set metadata;
  a JSON array alone cannot prove a set or list promise.
- Maps and collections containing `any` must have a common element type without
  primitive coercion. A mixed number/string collection cannot manufacture a
  string guarantee by converting numbers. Known siblings of unknown values
  still undergo validation.
- Consumer checks first apply the module's variable type, nullable/default
  behavior, optional defaults, and normal input conversion in memory. Contracts
  then inspect that effective value without performing another conversion.
  The original managed input is still passed to Terraform/OpenTofu, which owns
  final conversion and module validation. Chosen external tfvars and environment
  inputs are obtained from saved-plan JSON, not guessed from managed inputs.
- Runtime and module sensitivity cannot be downgraded. Diagnostics include port
  names and conditions, never payloads or nested keys.

A successful null root output is omitted by native `output -json`. terragraph
restores it only when that same successful apply (or no-change plan) establishes
null. Computed outputs that become null without plan-time null evidence remain
unavailable too. A missing live output after restarting, selecting only a downstream node,
continuing a saved frontier, or recovering outputs is **not** proof of null.
Run the upstream and consumer in one ordinary apply to obtain current evidence.
A snapshot can preserve an observed null, but is used only after a failed live
read under the existing opt-in fallback policy; successful reads with a missing
port never fall back. This limitation is deliberate: removed outputs and
missing state must not silently become null inputs.

Plan review JSON includes `review.contracts`, an array of `port`, `condition`,
and `result`. Results are `confirmed`, `violation`, `deferred`, or
`unconstrained`. Type, nullability, and sensitivity are separate conditions;
confirmation is not proof that provider validation or apply will succeed.
Ordinary text plans also inspect contracts when a saved plan is supported.
Enhanced remote/cloud backends keep their text preview fallback and report
contract evidence as deferred.

No-change apply, retained-plan apply, live upstream reads, snapshots, output
recovery, and parallel execution use the same value checks. Consumed and
unconsumed explicit producer ports are checked when observed. A post-apply
violation leaves the journal in `applied`, awaiting output recovery; it does
not reapply the producer. Independent nodes already running in parallel are
not rolled back. Destroy checks consumed upstream values and, for explicitly
contracted consumers, inspects effective inputs in a saved destroy plan; it
never requires output promises from the outputs being deleted.

## Adoption and compatibility

Earlier releases enforced declarations only. Existing `enforce` blueprints can
now reject values previously accepted. Start with `warn`, inspect runtime
findings, correct contracts or module values, then enable `enforce`. Neither a
new strictness flag nor a separate coercion/default language is needed.

Contract semantics and mode are included in retained-plan bindings. Recreate
plans made by older versions or before a contract change. Snapshot schema 3
preserves runtime type metadata; schema 2 remains readable but cannot prove
collection kind, and schema 1 values remain withheld. Older binaries that do
not understand schema 3 cannot consume new snapshots. Reobserve and regenerate
artifacts when moving between versions; downgrading does not undo infrastructure.

Ephemeral inputs omitted from saved-plan JSON cannot establish an additional
value requirement; enforce blocks these checks with C011. Dynamic edges and
managed vars cannot supply module `const` inputs before initialization; configure
those through the runtime's supported initialization inputs instead. Unevaluable
module defaults and missing runtime metadata remain unverified, not successful.
Contracts do not replace provider checks, variable validation, IAM checks, or
business rules such as valid CIDRs. Normal type checks remain active even when
no explicit consumer contract is written.

Provider-free regression fixtures cover Terraform 1.5.7 and 1.16.0, and OpenTofu
1.11.0. This is a tested matrix, not a change to the project's minimum runtime
version. An enterprise-root usability pilot is still needed before claiming
coverage of most infrastructure teams; passing these fixtures is not that claim.
