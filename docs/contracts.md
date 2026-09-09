# Contracts

Contracts help review whether connected modules agree on the values they
exchange. A producer declares an output's promised type, nullability, or
sensitivity; a consumer declares what its input expects. They are useful when
reusing modules or reviewing an interface change before execution.

Contracts check **declarations**, not actual output values. Even in `enforce`
mode, terragraph does not verify that a returned value satisfies a producer's
type or non-null promise. Runtime input type checks use the module's variable
declaration, not a narrower consumer contract.

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
    type      = "string"
    nullable  = false  # promises a non-null output
    sensitive = false
  }
}

consumer "./modules/app" {
  input "vpc_id" {
    type      = "string"
    nullable  = false  # requires the producer's non-null promise
    sensitive = false
  }
}
```

The module's input must accept `string`, and its input and output sensitivity
declarations must agree with the explicit `sensitive = false` claims above.
See the [contracts example](../examples/contracts) for a complete blueprint
and modules.

Each port accepts three optional attributes. `type` is a quoted Terraform
type constraint, such as `"string"` or `"list(string)"`.

| Omitted attribute | Producer | Consumer |
|---|---|---|
| `type` | No type promise | No type requirement |
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
command. Once the declarations agree, make findings block commands by adding
this top-level block:

```hcl
contracts {
  mode = "enforce"
}
```

`enforce` makes every C001–C009 finding an error for `validate`, `graph`,
`plan`, `apply`, and `destroy`. Set `mode = "warn"` or remove the mode block
to return to warnings. There is no per-code severity setting. Invalid syntax
and conflicting contract declarations remain errors in either mode.

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
| Producer `type` | Not checked against the output's value |
| `nullable` | Compared between contracts only, not with module declarations or values |

A consumer contract can be narrower than the module's input type. For example,
a module variable declared as `map(any)` can have this consumer contract:

```hcl
consumer "./modules/app" {
  input "tags" { type = "map(string)" }
}
```

C007 accepts that narrowing, but reports `string` against a `number` variable:
not every string can be converted to a number. The narrower contract documents
the intended interface; it does not add runtime input validation.

## Error classes and codes

All codes are warnings in `warn` mode and errors in `enforce` mode.

| Code | Reported when |
|---|---|
| C001 | Producer contract names an output the module does not declare |
| C002 | Consumer contract names an input variable the module does not declare |
| C003 | Producer type cannot be converted to the consumer's required type |
| C004 | Consumer requires non-null but the producer does not promise `nullable = false` |
| C005 | Producer declares `sensitive = true` but the consumer does not |
| C006 | Contract source matches no node in the graph; update the source or remove the contract |
| C007 | Consumer's claimed type cannot be safely converted to the module variable's declared type |
| C008 | Consumer's explicit sensitivity differs from the module variable's declaration |
| C009 | Producer's explicit sensitivity differs from the module output's declaration |

## Machine-readable validation

`validate --output json` returns contract identifiers such as `C001` and `C009` directly in `problems[].code`, with `category: "validation"`, `phase: "validation"`, `subject`, `severity`, and `remedy`. Consumers do not need to extract bracketed codes from human messages. The existing messages retain their bracketed codes for text users.

Contract warnings still allow `valid: true`; only enforce mode makes them errors. Preflight validation failures in graph and execution commands include the actual structured problems in `diagnostics`, so an agent does not need to invoke a second command just to identify the problem. See [agent usage](agent-usage.md).
