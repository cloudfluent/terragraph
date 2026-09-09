# Blueprint

A blueprint describes independent Terraform/OpenTofu root modules (`node` blocks) and the connections between them (`edge` blocks). The CLI reads the current directory by default, merging its `.hcl` files except `.terraform.lock.hcl`; select a single file or another directory with `--blueprint`.

```hcl
node "vpc" {
  source = "./stacks/vpc"
}

node "eks" {
  source = "./stacks/eks"
}

edge {
  from = node.vpc.output.vpc_id
  to   = node.eks.input.vpc_id
}
```

Each `source` points to a module with its own Terraform configuration, variables, and outputs. Local node sources start with `./` or `../` and resolve relative to the blueprint file's directory, or to the directory selected by `--blueprint`. Other source strings require [vendoring](vendoring.md). Node, group, runtime, and `use` instance names may contain only letters, digits, underscores, and hyphens.

An edge can carry a value or only control execution order:

- **Data edge:** `node.<name>.output.<attr>` feeds `node.<name>.input.<attr>`. Both ports must exist in their modules. The downstream node waits for the upstream node, then receives its real output value.
- **Ordering-only edge:** `from = node.vpc` and `to = node.eks` makes `eks` wait for `vpc` without passing a value.

Both endpoints must use the same form; mixing a bare node and a port is an error. A destination input may have only one data source, including after [group expansion](groups.md). Duplicate data edges and a data edge combined with `vars` on the same input are errors. Ordering-only edges do not occupy an input.

See [`examples/basic`](../examples/basic) for runnable modules and [the execution model](execution-model.md) for how `plan`, `apply`, and `destroy` use these connections.

## Literal input values (`vars`)

Use `vars` for values specific to a node, such as an environment name or CIDR. Keys are the module's declared variable names:

```hcl
node "data-apne2-dev-vpc" {
  source = "./modules/vpc"
  vars = {
    name            = "dpl-apne2-vpc-dev"
    cidr            = "10.16.0.0/20"
    private_subnets = ["10.16.0.0/23", "10.16.2.0/23"]
    tags            = { tenant = "data-platform" }
  }
}
```

Values can be strings, numbers, booleans, nulls, lists, or nested objects. Numeric precision is preserved when values are passed to Terraform. If you control the module interface, related settings can share an `object`-typed variable instead of many separate variables.

`vars` accepts literal data with no variables or functions in scope. Another node's output needs an `edge`; putting `node.other.output.x` inside `vars` is an error and would not record the dependency. A `vars` key must name a real input and must not also be supplied by a data edge.

Resolved `vars` and edge values share the same temporary tfvars file and type checks. Terraform/OpenTofu performs the final conversion and variable validation. See [how values are passed](execution-model.md#how-values-are-passed) for the optional `tfvars` setting and cleanup behavior, and [group instance inputs](groups.md#setting-literal-inputs-for-an-instance) for `use.vars`.

## Several values between the same two nodes (`input`)

When two modules exchange several values, nested `input` blocks let you name the nodes once:

```hcl
edge {
  from = node.vpc
  to   = node.eks

  input "vpc_id" {
    from = output.vpc_id
  }

  input "subnet_ids" {
    from = output.private_subnet_ids
  }
}
```

Each block label names an input on the destination, and `from = output.<attr>` names an output on the source. Use this relative spelling inside the block; an absolute `node.vpc.output.vpc_id` is rejected. Labels must be unique within an edge.

Each `input` becomes an ordinary data edge, so the same port checks, input collision rules, and execution order apply. The enclosing `from` and `to` must be bare node or `use` references. With no `input` blocks, the edge is ordering-only; it cannot combine nested inputs with port endpoints.

Either endpoint may be a group instance, such as `use.checkout`. Its values resolve through the group's [exported ports](groups.md), including input fan-out.

## Files and loading

| Invocation | Files read |
|---|---|
| `terragraph graph` | Eligible `.hcl` files directly in the current directory |
| `terragraph graph --blueprint topology.hcl` | `topology.hcl` only |
| `terragraph graph --blueprint .` | Same as the default |
| `terragraph graph --blueprint ./config` | Eligible `.hcl` files directly in `./config` |

Directory loading is non-recursive and excludes `.terraform.lock.hcl`. Other hidden `.hcl` files are included; keep another tool's HCL configuration in its own directory. `.tf`, `.HCL`, and `.hcl.json` files are not collected. Subdirectories are visited only through module and group source references.

All configuration-loading commands use this default: `validate`, `graph`, `plan`, `apply`, `destroy`, `output`, `status`, `vendor`, and `force-unlock`. A selected directory with no eligible configuration files is an error, including one containing only Terraform files or `.terraform.lock.hcl`. An empty configuration file or a file containing only group definitions remains valid; having no executable nodes is distinct from having no configuration files.

Previously, commands without `--blueprint` read only `blueprint.hcl`. They now also include sibling files such as `contracts.hcl`, which may add nodes, settings, or duplicate declarations. Pass `--blueprint blueprint.hcl` to retain single-file loading. Explicit file selection reads exactly that file, without applying the directory filename filter.

You can split a blueprint without creating either `blueprint.hcl` or `group.hcl`. In a new directory, copy the `stacks` directory from [`examples/basic`](../examples/basic) and create these two files:

```hcl
# nodes.hcl
node "vpc" { source = "./stacks/vpc" }
node "eks" { source = "./stacks/eks" }
```

```hcl
# edges.hcl
edge {
  from = node.vpc.output.vpc_id
  to   = node.eks.input.vpc_id
}
```

```sh
terragraph graph
# level 1: vpc
# level 2: eks
```

Names and singleton settings must be unique across the merged files; later files do not override earlier declarations. The files may contain any supported top-level blocks regardless of their names. Block nesting still matters: `export` belongs inside a `group`, while settings such as `runtime`, `vendor`, and `lock` belong at the top level. Repeating the same `group` name in multiple files is a duplicate definition, not a way to extend its body. See [group source directories](groups.md#source-directories-and-filenames) for how a `use` selects a group.

## Reusing the same module across instances

Multiple nodes can share one `source` while supplying different `vars`, environments, and backend settings. Each instance needs its own state address.

For a module declaring `backend "local"` (an empty block is enough), terragraph supplies an absolute `path` of `<blueprint dir>/.terragraph/state/<node>.tfstate` when the node has no explicit `backend_config.path`:

```hcl
node "vpc_prod" {
  source = "./stacks/vpc"
}

node "vpc_dev" {
  source = "./stacks/vpc"
}
```

This gives the default workspace a different state file for each node. A module with no backend block uses Terraform's implicit local state and does not receive this generated path. An explicit `backend_config.path` wins; a path written only in the module's backend block is overridden. A relative path is resolved from the **module's directory**, not the blueprint directory. Non-default Terraform workspaces use their backend's workspace paths, so verify those separately. Terragraph never migrates existing state; see [state paths and migration](execution-model.md#how-values-are-passed) before adopting this layout for an existing deployment.

An explicit relative local-backend `backend_config.path` is passed through unchanged. For example, with `source = "./modules/vpc"`, `path = ".terragraph/state/vpc.tfstate"` places default-workspace state under `modules/vpc/.terragraph/state/`. `terragraph validate` warns about this relative path. Omit `backend_config.path` to use the automatic absolute path above, or supply an absolute path outside the module directory when choosing a custom location.

Use `backend_config` for remote backend fields such as `bucket`, `key`, `region`, and `profile`, or for an explicit local path. Entries are passed to `terraform init` as `-backend-config` options. A non-empty map requires a `backend` block in the module; it is invalid with no backend block or with a `cloud` block. A group's [`use.backend_config`](groups.md#isolating-state-for-an-instance) can supply shared defaults.

An optional `backend_address` object on a `node` or `use` generates a missing S3 `key` from literal prefix and file-name fields:

```hcl
node "database" {
  source = "./modules/database"
  backend_config = {
    bucket = "example-state"
    region = "ap-northeast-2"
  }
  backend_address = {
    s3_key_prefix = "prod"
    s3_key_name   = "terraform.tfstate"
  }
}
```

For a module declaring `backend "s3"`, this produces `prod/database/terraform.tfstate`. Only `s3_key_prefix` and `s3_key_name` are supported; both are optional, but at least one must be present to enable generation. The construction is `[prefix/]<qualified-leaf>/<file-name>`, where the qualified leaf directory is always included automatically. Values are literal strings, with no placeholder substitution, functions, or references to other nodes.

| Field | Default when generation is enabled | Meaning |
|---|---|---|
| `s3_key_prefix` | Empty | Literal object-key prefix, such as `prod` or `prod/team`. A trailing `/` can supply the separator before the leaf directory. |
| `s3_key_name` | `terraform.tfstate` | Non-empty file name inside each leaf directory. `/`, `\`, `.` and `..` are not accepted as path separators or directory names. |

Omitting `backend_address` inherits the enclosing `use` rule. Fields merge independently: an inner `use` overrides an outer one, and a node's fields win. Setting `s3_key_prefix = ""` clears an inherited prefix while retaining an inherited file name. Setting `backend_address = {}` disables inherited generation for that scope; a descendant may enable it again with either field.

Generation runs only after existing `backend_config` inheritance is resolved, and only if `key` is absent both from that configuration and from the module's backend block. An explicit key always wins, including an inherited key or a module key whose value cannot be evaluated statically. Empty explicit keys are passed through rather than repaired. If the module cannot be inspected enough to establish key absence, terragraph requires an explicit key or disabling generation. Changing only `s3_key_name` never replaces an explicit `backend_config.key`.

This convenience currently generates only the S3 `key` field. Other backend types ignore these generation fields and keep their existing configuration, including local default paths. The rule does not select a backend type or configure a bucket, credentials, workspaces, or locking. Existing static collision checks apply to generated and explicit addresses alike; inheriting one explicit key across several leaves can still be a collision. These checks retain their existing limitations for unknown backend settings and namespaces.

Renaming a node or group instance, moving a leaf into a different group, or changing the selected prefix or file name can change its generated state address. Terragraph does not migrate state automatically. Preserve an existing location by setting its exact `backend_config.key` before adopting or changing a generation rule. Merely renaming a group's declaration without changing instance names or its leaf structure does not change the qualified leaf names.

Validation rejects identical backend configuration maps on a shared source, known shared local state paths, and known shared S3 state addresses. It also rejects node names that collide on the filesystem, including case differences on a case-insensitive volume. These checks use statically known settings, including explicit `TF_WORKSPACE` values; they cannot resolve all backend expressions, external configuration, or credential-dependent namespaces. Warnings about unverified separation require checking the paths or backend namespaces yourself. Set distinct addresses and explicit region/endpoint settings where needed; a different profile alone does not establish a different state address.

Every node receives a separate `TF_DATA_DIR` for Terraform's backend metadata, even when sources are not shared. The module's `.terraform.lock.hcl` is still shared by nodes using the same directory. Validation warns when those nodes select different runtime binaries; use separate source copies or the same runtime to avoid provider lock file conflicts.

## Choosing a runtime per node (`runtime`)

Use named runtimes to select Terraform or OpenTofu per node, or to pin a binary while migrating stacks independently:

```hcl
runtime "tofu" {
  binary  = "tofu"
  version = ">= 1.8.0"  # documentation only; not an enforced constraint
}

runtime "legacy" {
  binary = "/opt/terraform_1.5.7"
}

node "eks" {
  source  = "./stacks/eks"
  runtime = runtime.tofu
}

node "legacy_dns" {
  source  = "./stacks/dns"
  runtime = runtime.legacy
}
```

Runtime selection uses the node's explicit choice, then the nearest enclosing [`use.runtime`](groups.md#choosing-a-runtime-for-an-instance), then the root blueprint's runtime marked `default = true`, then the CLI's `--tofu` flag or built-in `terraform` default. At most one runtime in the root blueprint may declare `default = true`. A group's default-marked runtime does not become an instance default.

`binary` may name a command on PATH or an absolute executable path. `version` records intent; terragraph does not enforce that constraint. Before switching runtimes, back up state and check compatibility: new state formats or runtime features can prevent a rollback. See [Terraform's compatibility guidance](https://developer.hashicorp.com/terraform/language/v1-compatibility-promises) and the [OpenTofu migration guide](https://opentofu.org/docs/intro/migration/migration-guide/).

Validation and editor inspection follow the same runtime choice. Terraform reads `.tf` and `.tf.json`; OpenTofu also reads `.tofu` and `.tofu.json`. An OpenTofu file replaces only its same-name, same-format Terraform counterpart, so `main.tofu` replaces `main.tf`, while `main.tofu.json` and `main.tf` coexist. Inspection does not execute a runtime or edit module files.

The canonical `terraform` and `tofu` commands identify the inspection mode. An absolute binary path does too if it resolves to the same file as exactly one of those commands on PATH. Other binaries and wrappers are accepted only when both modes expose identical static declarations, including defaults, sensitivity, and backend settings. If inspection reports a mismatch, select the intended canonical command on PATH, point to that installation, or make the declarations agree; a wrapper's filename alone does not identify its runtime.

Recognized OpenTofu runtimes selecting `.tofu` files must report version 1.8.0 or newer before `plan`, `apply`, or `destroy` can consume inputs. An old, failed, or unparseable version response stops execution. A `--node` run also checks direct upstream nodes whose outputs it may read. Modules selecting only `.tf` files need no probe; unknown wrappers with matching declarations remain allowed without a probe.

## Extra environment variables per node (`env`)

Use `env` when nodes need different provider accounts, regions, or roles:

```hcl
node "prod_vpc" {
  source = "./stacks/vpc"
  env = {
    AWS_PROFILE = "prod"
    AWS_REGION  = "ap-northeast-2"
  }
}
```

Entries override the terragraph process's environment for that node's Terraform/OpenTofu subprocesses. An enclosing [`use.env`](groups.md#setting-the-environment-for-an-instance) contributes defaults; the node overrides only the keys it sets. Nested instances merge in the same way, with the nearest declaration winning.

`TF_DATA_DIR` is reserved for per-node backend isolation. Declaring it in node or `use` `env` is an error, regardless of case or an empty value. Remove that entry; terragraph supplies its own value, including when the host environment or a custom runtime is used.

Terragraph never generates provider configuration. If a provider setting needs a module variable instead of an environment variable, supply it through `vars` or an edge.

<a id="how-much-a-node-may-change-without-being-asked-approve"></a>

## Allowed plan changes (`approve`)

Set `approve` to control which resource changes a node may apply:

```hcl
node "db" {
  source  = "./stacks/db"
  approve = "safe"
}
```

| Level | Allowed resource changes |
|---|---|
| `none` | No resource mutations |
| `safe` (default) | Create and update |
| `all` | Create, update, replace, and delete |

The policy is checked before applying, including in interactive runs. `--auto-approve` skips confirmation prompts; it does not bypass this policy. A plan that exceeds the policy fails before that node applies, and later levels do not run.

`none` can still apply plans containing only output changes or reads, which may write state. Use `terragraph plan` for a preview without apply.

The node's setting wins over the nearest enclosing [`use.approve`](groups.md#setting-an-approval-policy-for-an-instance), then `--approve`, then `safe`. Thus `--approve=all` only changes the fallback for nodes with no declared policy; it cannot override the explicit `safe` above. See [approval behavior](execution-model.md#what-a-node-may-do-approve) for execution details and how this applies to `destroy`.

## Graph remote lock (`lock`)

Add a top-level `lock` block when laptops, CI jobs, or separate clones may run the same graph concurrently. It serializes `plan`, `apply`, and `destroy` across machines while Terraform continues to lock each node's state:

```hcl
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
```

The block itself enables locking. A blueprint may declare one lock, containing one `s3` block; other lock backends are not supported. `bucket`, `key`, and `region` must be non-empty literal strings.

Every node must then use a supported remote backend (`s3`, `gcs`, `azurerm`, `http`, `remote`, or `cloud`); implicit or explicit local state is rejected. Choose a lock object separate from every node's state. Validation checks known S3 addresses and reports collisions or unverified separation; resolve these before sharing the graph across machines. Without the block, local state remains allowed.

See [graph remote locking](execution-model.md#graph-remote-lock) for credentials, permissions, contention, and recovering a stale lock, and [backend limitations](execution-model.md#known-limitation) for `apply` support.

## Execution record storage (`execution`)

Use an optional root-level `execution` block to select local or S3 record storage and configure retained-plan and completed-record lifetimes. It does not change Terraform state backends or enable retained-plan execution by itself. See [execution records](executions.md) for the configuration and `plan list`/`plan show` commands.

## Output snapshots (`snapshots`)

Add an empty top-level block to retain local output values for fallback when live upstream outputs are unavailable:

```hcl
snapshots {}
```

A blueprint may contain one `snapshots` block; it accepts no settings. During `apply`, including a no-change apply, terragraph records outputs consumed by data edges under `.terragraph/outputs/`. Sensitive outputs and outputs without sensitivity metadata are withheld; only their names are recorded. Keep `.terragraph/` out of version control.

Snapshots are a last resort after this run's applied outputs and live `terraform output`. They may be stale and do not replace a refreshed plan or automatically apply upstream nodes. Without the block, snapshots are neither written nor used. See [output snapshots](execution-model.md#output-snapshots) for fallback conditions and refreshing existing snapshots.

For additional checks on values exchanged between modules, see [producer and consumer contracts](contracts.md). Contract types accept native expressions such as `type = list(string)` and legacy strings. Consumer type omission retains the module variable's type check. Explicit contracts add actual-value checks during plan/apply, with `warn` as the default and `enforce` to block violations.
