# Blueprint

A blueprint (`blueprint.hcl` by default) is a flat list of `node` and `edge` facts, not nested configuration. `--blueprint` can also point at a directory instead of a single file: every `.hcl` file directly inside it (not recursively) is merged into one blueprint, exactly the way a [group](groups.md)'s source directory already merges its own `.hcl` files. This is useful for splitting a large blueprint across several files (e.g. `nodes.hcl`, `edges.hcl`) without any of them needing a specific name.

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

An edge can be:
- **Explicit (data edge)**: both sides reference a specific port (`node.<name>.output.<attr>` / `node.<name>.input.<attr>`). The engine passes the value at runtime.
- **Implicit (ordering-only edge)**: both sides reference a bare node (`node.<name>`). No value is passed; it only constrains execution order.

Mixing the two on the same edge (a port on one side, a bare node on the other) is rejected at parse time.

An input is a single slot: two data edges targeting the same input are a validation Error, whether their `from` sides differ or match (exact duplicates). The same rule covers a data edge colliding with `vars` (see [Literal input values (`vars`)](#literal-input-values-vars)). It is checked after group expansion, so two outer edges, or an outer edge plus an internal one, that only meet on the same leaf once a `use` export has rewritten them are caught too. A `use.vars` key is rewritten onto those same leaves and collides the same way as `node.vars`; see [groups.md](groups.md#setting-literal-inputs-for-an-instance). Ordering-only edges are not this rule; they carry no value.

Node canvas layout (for the future visual editor) lives in a separate `blueprint.layout.json`, so moving a box never shows up in a `blueprint.hcl` diff.

## Several values between the same two nodes (`input`)

One `edge` block is one pair, which is faithful to "a flat list of facts" but noisy when two modules that already expose many flat variables need ten of them wired. An edge whose `from` and `to` are bare references may instead carry nested `input` blocks, the same shape as a group's [`export` input](groups.md):

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

The block label is the destination input on the `to` node, and `from` is a relative `output.<attr>` on the `from` node: the enclosing edge already names the source, so it is never repeated (an absolute `node.vpc.output.vpc_id` inside the block is rejected rather than accepted-if-it-matches). Duplicate labels on one edge are an error, the same as in an `export` block.

This is shorthand and nothing more. With no `input` blocks the edge is the ordering-only edge above; with one or more, it expands at parse time into exactly that many ordinary data edges (`node.vpc.output.vpc_id` → `node.eks.input.vpc_id`, and so on), and everything downstream — the one-source-per-input rule below, `use` export resolution, cycle detection — treats them as if they had been written out one block each. An edge that already names a specific port on either side has exactly one value to carry, so combining that with `input` blocks is rejected.

When you own both modules, the better reduction is still one `object`-typed output wired into one `object`-typed input by one ordinary edge (see [`vars`](#literal-input-values-vars) for the same argument applied to literal values). This shorthand is for the modules you don't get to reshape.

Either endpoint may be a group instance (`use.<name>`), and each expanded edge then resolves through that instance's `export` exactly as a separately written one would, fan-out included: see [groups.md](groups.md).

## Reusing the same module across instances

A node can optionally set `backend_config` to reuse one module `source` across multiple instances (e.g. the same `./stacks/vpc` for both `dev` and `prod`) without their state colliding:

```hcl
node "vpc_prod" {
  source = "./stacks/vpc"
  backend_config = {
    path = ".terragraph/state/vpc_prod.tfstate"   # local backend example
  }
}
```

This is passed straight through to `terraform init -backend-config=key=value` (Terraform's own partial backend configuration mechanism, not code generation). It requires the module to declare at least an empty backend block (`terraform { backend "local" {} }`); with no backend block at all there's nothing for `-backend-config` to apply to and it's silently ignored. Every node also gets its own isolated `.terraform/` metadata directory (`TF_DATA_DIR`, managed automatically) regardless of whether its `source` is shared with another node. Otherwise two instances of the same module would also fight over which backend they were last configured with.

Sharing a `source` directory does *not* isolate `.terraform.lock.hcl`, though: unlike `.terraform/`, that file lives in the module directory itself. If those instances also resolve to different runtimes (see below), Terraform and OpenTofu will each keep rewriting it to their own provider registry host on every `init`, and `terragraph validate` warns about exactly this. Give each instance its own `source` copy (or wait until they're all on the same runtime) rather than ignoring that warning.

## Choosing a runtime per node (`runtime`)

Every node runs against whichever binary a plain `--tofu`/no-flag choice on the CLI selects, by default. A node (or a whole blueprint) can override that by declaring one or more named `runtime` blocks and referencing one:

```hcl
runtime "tofu" {
  binary  = "tofu"          # a PATH-resolved command, or an absolute path to pin an exact install
  version = ">= 1.8.0"      # optional, free-form; documentation only, see below
}

runtime "legacy" {
  binary  = "/opt/terraform_1.5.7"
}

node "eks" {
  source  = "./stacks/eks"
  runtime = runtime.tofu     # this node always runs on tofu, regardless of --tofu
}

node "legacy_dns" {
  source  = "./stacks/dns"
  runtime = runtime.legacy   # pinned to an exact binary, for a stack that isn't ready to move yet
}
```

A node that names no `runtime` falls back, in order: the blueprint's own `default = true` runtime, if it declared one; otherwise the CLI's `--tofu` flag or its built-in `terraform` default. Since every node has its own isolated state (see [execution-model.md](execution-model.md)), this can be applied node by node: migrate one stack to a new runtime while everything else keeps running exactly as before, with no shared workspace to force an all-or-nothing cutover. There's no way back down, though: Terraform/OpenTofu record the version that last wrote a state file, and an older binary will refuse to read it, so treat this as a one-way door per node, not something to toggle back and forth.

`version` is never checked against the binary's actual reported version; nothing runs `terraform version` to confirm it, and it has no effect on execution. It is there to record what a node is expected to run against, next to the `binary` that actually selects it. (It once fed the incremental-apply cache key. That cache is gone: whether a node needs applying is now decided by asking Terraform for a refreshed plan, every run — see [execution-model.md](execution-model.md#deciding-whether-a-node-needs-applying).)

### How much a node may change without being asked (`approve`)

```hcl
node "db" {
  source  = "./stacks/db"
  approve = "all"
}
```

`approve` declares how much of this node's plan may be applied unattended: `none`, `safe` (create/update, the default), or `all` (adds replace and delete). Set it on the specific node whose plan is destructive by design, rather than reaching for `--approve=all`, which grants the same thing to every node in the graph at once and tends to end up pasted into CI.

Like `runtime`, it replaces rather than merges, and a `use` block can set it for every node an instance expands to. Full resolution order and what happens when a plan exceeds its level are in [execution-model.md](execution-model.md#what-a-node-may-do-approve).

A `use` block can also set `runtime`, which becomes the default for every node the group instance expands to (unless one of those nodes names its own): see [groups.md](groups.md#choosing-a-runtime-for-an-instance). A group's own definition has no equivalent: which toolchain deploys a reusable group is a fact about where it's instantiated, not about the group itself.

## Extra environment variables per node (`env`)

Different nodes sometimes need to run against different cloud accounts, regions, or roles: the same module deployed once per tenant, each into its own AWS account, say. That's ordinarily expressed through whatever a provider block reads from its environment (`AWS_PROFILE`, `AWS_REGION`, `ARM_SUBSCRIPTION_ID`, and so on), so a node can set `env` to add exactly those, keyed by variable name:

```hcl
node "prod_vpc" {
  source = "./stacks/vpc"
  env = {
    AWS_PROFILE = "prod"
    AWS_REGION  = "ap-northeast-2"
  }
}
```

Each entry is added to (and, on a name collision, overrides) the terragraph process's own environment before the node's `terraform`/`tofu` subprocess starts; nothing else about that environment is touched. This is deliberately the *only* mechanism terragraph offers for this: it never generates or edits a `provider` block (see [execution-model.md](execution-model.md#how-values-are-passed) for the same "no generated `.tf`" rule applied to values), so a provider that needs something `env` can't express (a literal value baked into the config, say) should instead read it as a variable and take that through an edge or `vars`, the same as any other input.

Unlike `runtime` (a single choice that replaces whatever it inherits), `env` merges: a `use` block's own `env` (see [groups.md](groups.md#choosing-a-runtime-for-an-instance)) contributes defaults to every node the instance expands to, and a node's own `env` only overrides the specific keys it names, leaving everything else it inherited in place. `env` is part of what a node actually runs against, so changing which account or region it targets changes the plan Terraform produces for it — see [execution-model.md](execution-model.md#deciding-whether-a-node-needs-applying).

## Literal input values (`vars`)

An edge wires one node's real output into another node's input, but not every input is another node's data. Sometimes a value is just this node's own: "this tenant's CIDR is 10.16.0.0/20." A node can set `vars` to supply such values directly, keyed by variable name:

```hcl
node "data-apne2-dev-vpc" {
  source = "./modules/vpc"
  backend_config = {
    path = ".terragraph/state/data-apne2-dev-vpc.tfstate"
  }
  vars = {
    name            = "dpl-apne2-vpc-dev"
    cidr            = "10.16.0.0/20"
    private_subnets = ["10.16.0.0/23", "10.16.2.0/23"]
    tags            = { tenant = "data-platform" }
  }
}
```

This is the same mechanism an edge uses to feed a value in: both end up merged into the same engine-managed tfvars file (see [execution-model.md](execution-model.md#how-values-are-passed)) and type-checked against the target variable's declared type the same way, so a `vars` value is exactly as safe as a wired one. An input is a single slot: it's an error for it to be set by more than one source at once, whether that's two data edges targeting the same input (including exact duplicates, and including after group expansion, where two outer edges or an outer edge plus an internal one can converge on the same leaf only once a `use` export has rewritten them) or a data edge and `vars` together. Since a variable's value can itself be any JSON-compatible shape a Terraform variable can hold (string, number, bool, list, or a nested object like `tags` above), a module needing many inputs still only needs one `vars` entry per node, not one edge per variable: give it a single variable typed `object({ ... })`, or several logically-grouped ones, rather than dozens of flat variables, and reuse across many nearly-identical stacks (the same module, one per tenant, differing only in `vars`) stays as small as adding one `node` block each.

`vars` is for literal data, not another node's output: the attribute is evaluated with no variables or functions in scope, so writing `node.other.output.x` inside it fails to parse. That value needs a real edge, which is what actually records the dependency and makes the engine wait for it to exist.

See also: [groups.md](groups.md) for bundling several nodes into one reusable unit, [vendoring.md](vendoring.md) for pointing `node.source` at a remote module, and [execution-model.md](execution-model.md#how-values-are-passed) for the optional `tfvars` block controlling where a node's resolved values are written.
