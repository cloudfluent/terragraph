# Contracts example

Two nodes, one data edge, one two-sided contract declared as top-level
blueprint blocks. Run:

    terragraph validate --blueprint examples/contracts/blueprint.hcl

prints no contract warnings. Flip the consumer's `type` to `list(string)` and
validate reports `contract.[C003] ...` (plus `contract.[C007] ...` — the
flipped claim now contradicts the module as well) as warnings — advisory in
this phase (`number` would not fire C003: string to number is a lossy
conversion, not an incompatibility). Flip the app module's `vpc_id` variable
while the contract still says `string` and validate reports
`contract.[C007] ...`: the module is the declaration of record, so the
contract is the side to fix.

See `docs/contracts.md` for the grammar and the full C001–C011 code table.

Types can be written directly (`type = string`); legacy strings remain valid.
`validate` checks declarations, while plan/apply also check explicit contracts
against real outputs and effective module inputs. Omit a consumer `type` when
its module variable already expresses the requirement. Enable `enforce` only
after reviewing the default `warn` diagnostics.
