# Agent usage

An agent can use the same CLI as a person. Default output remains text; select `--output json` explicitly for structured results. No agent detection, separate runtime, or agent-specific plugin is required. These examples apply to callers such as Claude Code and Codex; actual organization and cloud pilots are separate from this CLI contract.

```sh
terragraph validate --output json
terragraph graph --output json
terragraph status --output json
terragraph plan --output json
```

Use the existing blueprint runtime configuration or `--tofu` for OpenTofu. Terraform state and S3 backend behavior are unchanged. Execution-record storage is configured separately from state storage; see [execution records](executions.md).

## Results and errors

Read stdout as one JSON value and inspect the exit status. Stderr carries human diagnostics and runtime progress; it is not a machine-readable API. Errors normally exit 1. Native `run` retains its native exit-code behavior and does not support this JSON contract.

| Command | Result shape and identity |
| --- | --- |
| `validate` | `schema_version`, `valid`, `problems`; no execution ID |
| `graph` | `schema_version`, `levels`, `diagnostics`; no execution ID |
| `status`, `output` | Existing `schema_version`, `nodes`, `diagnostics`; no execution ID |
| Ordinary `plan`, `apply`, `destroy` | `schema_version`, `nodes`, `diagnostics`, optional `execution_id` |
| `apply --plan <id>` | Same run shape; `execution_id` is the selected execution |
| `plan --save`, `plan list`, `plan show`, `plan cancel` | Existing `schema_version`, `executions`, `diagnostics`; IDs are `executions[].id` |
| `plan recover` | Existing flat record `id`, with `schema_version` and `diagnostics`; a known partial record survives failure |
| `plan prune` | `schema_version`, `removed`, `diagnostics`; no new execution |
| `vendor` | Array with per-node diagnostics; global failures use the exception below |

Object schemas remain version 1. Ignore unknown additive fields. Nodes retain existing order, status strings, and optional human `error` fields. Node-specific diagnostics belong beside the node; independent global failures, including final journal writes, appear in top-level diagnostics. Inspect both. A warning alone does not turn success into failure.

When no command result is available, an identifiable `--output json` request returns an error-only object:

```json
{
  "schema_version": 1,
  "diagnostics": [{
    "code": "invalid_arguments",
    "category": "arguments",
    "severity": "error",
    "phase": "arguments",
    "subject": "command",
    "message": "an argument is invalid"
  }]
}
```

Do not infer `valid: false` when blueprint loading failed: validation did not run. Plan, observation, and history retain their existing empty-result envelopes for failures inside their command handlers. Failures before dispatch use the error-only shape. The CLI emits at most one result, including when a JSON write fails; consumers must still handle missing or truncated output after process termination or an unwritable pipe.

Both `--output json` and `--output=json` are recognized using flag metadata, including when an earlier flag fails to parse. Values belonging to other flags and arguments after `--` are not output selection. Invalid or ambiguous output selection, help, version, and completion keep their existing behavior. `--raw` and `--backup` cannot be combined with JSON: conflicts fail before output values or native state are disclosed. `run`, `lsp`, and `force-unlock` do not gain JSON support.

Vendor keeps its array for success and node failures. A global failure with no results uses the error-only object; a global failure with partial results uses `{ "schema_version": 1, "results": [...], "diagnostics": [...] }`. This is a deliberate change to the nonzero-exit shape previously used for global vendor failures.

## Branch on diagnostics

Diagnostics have `code`, `category`, `severity`, `phase`, `subject`, and `message`. `remedy`, `related_execution_id`, and source location (`source.file`, `source.line`, `source.column`) are optional. Branch on code/category rather than parsing messages, remedies, or Terraform stderr. Existing codes, including plan inspection and observation codes, remain meaningful.

| Category | Caller action |
| --- | --- |
| `arguments`, `configuration`, `validation` | Correct the indicated input or declaration before retrying |
| `lock` | Resolve or wait for the active executor; do not automatically force-unlock |
| `policy` | Review changes and declared policy; the JSON flag does not grant approval |
| `recovery` | Inspect the referenced execution and actual state before continuing |
| `artifact` | Inspect saved-plan compatibility or expiry; create a fresh plan when instructed |
| `record` | Inspect the execution store; a failed write does not prove infrastructure was unchanged |
| `runtime`, `cancelled`, `unknown` | Inspect the execution and node outcomes before deciding whether retry is appropriate |

Common codes include `invalid_arguments`, `policy_blocked`, `recovery_required`, `execution_record_write_failed`, `execution_record_conflict`, `saved_plan_expired`, `saved_plan_incompatible`, `runtime_failed`, and `cancelled`. Graph problems expose codes such as `missing_input`, `missing_output`, `input_conflict`, `dependency_cycle`, and the existing contract identifiers `C001`–`C009`. Unknown codes must remain visible to the caller; they are not permission to retry automatically.

An `execution_id` comes from the existing acquired execution session. It is retained after later failures. `related_execution_id` can point to an earlier execution blocking progress or an allocated ID whose initial persistence failed; neither means a new executable session exists. Commands such as validate and status do not need IDs to report useful results.

## Review and mutate separately

`plan` returning `policy_decision: "block"` is a successful assessment, not a failed mutation. Ordinary apply replans; an ordinary inspection result is not an executable artifact. For retained plans, use the existing workflow:

```sh
terragraph plan --save --output json
terragraph plan show <execution-id> --output json
```

After the caller has authorized the intended mutation:

```sh
terragraph apply --plan <execution-id> --auto-approve --output json
```

JSON apply and destroy require `--auto-approve` to avoid hidden interactive prompts. Declared policy checks still apply. Never automatically replay an uncertain mutation or treat `cancelled`, a missing record, or a failed journal write as proof that nothing changed. No automatic recovery, retry, approval bypass, or sensitive-value disclosure is added by this contract.
