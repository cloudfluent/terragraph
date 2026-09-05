# Contracts example

Two nodes, one data edge, one undirected architectural relationship, and one
two-sided contract declared as top-level blueprint blocks. Run:

    terragraph validate --blueprint examples/contracts/blueprint.hcl

prints no contract warnings. Inspect the non-executable relationship overlay
with:

    terragraph graph --blueprint examples/contracts/blueprint.hcl --view relationships

which prints `relationship: app -- vpc`. The same source-keyed contracts that
review the data edge satisfy the relationship's C010 contract-presence check.

Flip the consumer's `type` to `list(string)` and
validate reports `contract.[C003] ...` (plus `contract.[C007] ...` — the
flipped claim now contradicts the module as well) as warnings — advisory in
this phase (`number` would not fire C003: string to number is a lossy
conversion, not an incompatibility). Flip the app module's `vpc_id` variable
while the contract still says `string` and validate reports
`contract.[C007] ...`: the module is the declaration of record, so the
contract is the side to fix.

See `docs/contracts.md` for the grammar and the full C001–C010 code table.
