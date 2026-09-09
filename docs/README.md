# Documentation

terragraph connects independent Terraform/OpenTofu root modules. A blueprint names the modules and connects their outputs to inputs; terragraph runs them in dependency order while each module keeps its own state.

## Start with a working example

[Install the CLI](../README.md#install) and put `terraform` or `tofu` on `PATH`. From a checkout of this repository, try the [basic example](../examples/basic), which uses local files and random values without cloud credentials:

```sh
cd examples/basic
terragraph validate
terragraph graph
terragraph apply
```

`validate` checks the blueprint against its modules, `graph` shows execution order, and `apply` plans and asks for confirmation as it reaches each changed node. Add `--tofu` to use OpenTofu. The example may download providers during initialization.

For a larger walkthrough, the [complete AWS landscape](../examples/complete) models six accounts, seven VPCs, three EKS platforms, and six applications using only built-in local fixtures. It combines nested groups and enforced contracts with runnable saved-plan, native-operation, and offline-vendoring labs.

For a new graph, `apply` can create upstream resources and pass their outputs to downstream nodes in the same run. `plan` reads existing upstream outputs, so it cannot preview a consumer whose required output is unavailable, or propagate upstream's newly planned values. Read the [planning limitation](execution-model.md#known-limitation) before using a whole-graph plan as a change preview.

For your own modules, start with [nodes, edges, and literal inputs](blueprint.md). If a node uses a remote source, run [vendoring](vendoring.md) before validation or execution. For a blueprint split across files, run commands in its directory or select another directory with `--blueprint <directory>`; see [files and loading](blueprint.md#files-and-loading).

## Find the next task

| You want to | Read |
|---|---|
| Connect modules, supply inputs, or choose runtimes and environments | [Blueprint](blueprint.md) |
| Reuse a set of connected modules | [Groups](groups.md) and the [group example](../examples/group) |
| Check producer and consumer declarations | [Contracts](contracts.md) |
| Bring Git sources into version control and update them | [Vendoring](vendoring.md) |
| Preview a graph, control changes, run in CI, or recover from failure | [Execution model](execution-model.md) |
| Use completion, definition navigation, and editor diagnostics | [VS Code IntelliSense](intellisense.md) |
| Inspect stored execution attempts and configure record storage | [Execution records](executions.md) |
| Scoped native init, console, import, and state operations | [Node operations](node-operations.md) |
| Integrate an LLM agent or other CLI automation | [Agent usage and JSON contract](agent-usage.md) |
| Look up a command or flag | [CLI reference](cli/terragraph.md), generated from the CLI |

Current-state reads are documented under [Observing outputs](execution-model.md#observing-outputs): `output`, redaction, backend support, partial results, and cache cleanup.
