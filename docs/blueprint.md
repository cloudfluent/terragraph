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

Node canvas layout (for the future visual editor) lives in a separate `blueprint.layout.json`, so moving a box never shows up in a `blueprint.hcl` diff.

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

This is the same mechanism an edge uses to feed a value in: both end up merged into the same engine-managed tfvars file (see [execution-model.md](execution-model.md#how-values-are-passed)) and type-checked against the target variable's declared type the same way, so a `vars` value is exactly as safe as a wired one. It's an error for a variable to be set by both an edge and `vars` at once. Since a variable's value can itself be any JSON-compatible shape a Terraform variable can hold (string, number, bool, list, or a nested object like `tags` above), a module needing many inputs still only needs one `vars` entry per node, not one edge per variable: give it a single variable typed `object({ ... })`, or several logically-grouped ones, rather than dozens of flat variables, and reuse across many nearly-identical stacks (the same module, one per tenant, differing only in `vars`) stays as small as adding one `node` block each.

`vars` is for literal data, not another node's output: the attribute is evaluated with no variables or functions in scope, so writing `node.other.output.x` inside it fails to parse. That value needs a real edge, which is what actually records the dependency and makes the engine wait for it to exist.

See also: [groups.md](groups.md) for bundling several nodes into one reusable unit, [vendoring.md](vendoring.md) for pointing `node.source` at a remote module, and [execution-model.md](execution-model.md#how-values-are-passed) for the optional `tfvars` block controlling where a node's resolved values are written.
