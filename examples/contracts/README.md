# Contracts example

Two nodes, one data edge, one two-sided contract. Run:

    terragraph validate --blueprint examples/contracts/blueprint.hcl

The graph is valid; the contract's producer and consumer agree, so validate
prints no contract warnings. Flip the consumer's `type` to `number` and
validate reports `contract.[C003] ...` as a warning — advisory in this phase.

See `docs/contracts.md` for the grammar and the full C001–C006 code table.

## Observe and propose

    terragraph observe  --blueprint examples/contracts/blueprint.hcl
    terragraph propose  --blueprint examples/contracts/blueprint.hcl

`observe` writes `terragraph.lock` (commit it); `propose` prints draft
contracts for any port the example does not cover yet — stdout only.
