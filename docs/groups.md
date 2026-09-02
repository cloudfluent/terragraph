# Groups: reusable sub-blueprints

A combination of nodes that's always used together (e.g. a cluster + its node group) doesn't need to be re-declared node-by-node for every new service. A `group` defines that combination once; `use` instantiates it under a new name:

```hcl
# groups/eks-service/group.hcl
group "eks-service" {
  node "cluster"   { source = "../../modules/cluster" }
  node "nodegroup" { source = "../../modules/nodegroup" }

  edge {
    from = node.cluster.output.cluster_id
    to   = node.nodegroup.input.cluster_id
  }

  export {
    input  "vpc_id"     { to   = node.cluster.input.vpc_id }
    output "cluster_id" { from = node.cluster.output.cluster_id }
  }
}
```

```hcl
# blueprint.hcl
node "vpc" { source = "./modules/vpc" }

use "eks-service" {
  as     = "checkout"
  source = "./groups/eks-service"
}

edge { from = node.vpc.output.vpc_id         to = use.checkout.input.vpc_id }
edge { from = use.checkout.output.cluster_id to = node.dns.input.cluster_id }
```

This expands purely in memory when the graph is built (no files are generated) into real nodes namespaced under the instance name (`checkout.cluster`, `checkout.nodegroup`), which every command (`plan`, `apply`, `--node`, `graph`) treats exactly like any other node.

A few rules fall out of that expansion:
- **Only `export`-declared ports are visible from outside the instance.** `use.checkout.output.cluster_id` works; `use.checkout.cluster.output.cluster_id` doesn't even parse. This is deliberate: a group is only safely reusable if its author can change internals without an unbounded set of external edges depending on them, the same reason Terraform modules, Go's unexported identifiers, and private class members all work this way. If a consumer needs something not currently exported, the fix is adding it to the group's `export` block, not reaching around it. Plain nodes (not inside a group) have no such restriction.
- **A data edge into a group's exposed input can fan out** (`to = [node.a.input.x, node.b.input.x]`) because "which internal nodes need this value" is a fact about the group's own design that its author must state; it isn't inferable from graph structure. An exposed output is always a 1:1 passthrough, so it never needs this.
- **A leaf input still takes at most one data edge after expansion.** Fan-out to distinct internal ports is not a collision. Two outer edges (or an outer edge plus an internal one) that rewrite onto the same leaf are a validation Error, including exact duplicates. This is the same one-source-per-input rule as a plain blueprint; see [blueprint.md](blueprint.md#literal-input-values-vars).
- **A bare, ordering-only edge into or out of a group needs no such declaration**: `edge { from = node.x, to = use.checkout }` (no port) expands automatically to every node inside the group with no internal predecessor (its "roots"); the symmetric case on the `from` side expands to every node with no internal successor (its "sinks"). Unlike fan-out, this is inferable directly from the group's internal graph shape.
- **An edge into or out of an instance can carry nested `input` blocks** (see [blueprint.md](blueprint.md#several-values-between-the-same-two-nodes-input)), wiring several of the instance's exposed inputs in one block: `edge { from = node.vpc, to = use.checkout, input "vpc_id" { from = output.vpc_id } ... }`. Each block expands into an ordinary data edge before any of the above applies, so an expanded edge fans out through `export` and collides on a leaf exactly as a separately written one would.
- **`node`↔`group` and `group`↔`group` edges use identical syntax** to `node`↔`node`. A group instance is indistinguishable from a node both in edge references and in schema: internal nodes are inspected exactly as today (`module.Inspect` against their real `.tf` files), the `export` block is validated against those real schemas, and once valid it's synthesized into the same schema shape a real module has. Groups can nest (a group's own `use` blocks resolve recursively), and a group that directly or transitively uses itself is a validation error.

## Choosing a runtime for an instance

A `use` block can set `runtime` (see [blueprint.md](blueprint.md#choosing-a-runtime-per-node-runtime)), which becomes the default for every node this instance expands to, unless one of those nodes names its own:

```hcl
use "eks-service" {
  as      = "checkout"
  source  = "./groups/eks-service"
  runtime = runtime.tofu   # both "checkout.cluster" and "checkout.nodegroup" run on tofu
}
```

This only ever comes from the instantiation site, never from the group definition itself. A group's own source directory can declare and reference its own `runtime` blocks internally (a specific internal node pinned to an exact binary, say), but it cannot mark one `default = true` and have that silently apply to every node it expands to: which toolchain deploys a reusable group is a fact about where it's used, not something the group's author gets to bake in, since that would make the group less reusable every time it's instantiated somewhere with a different toolchain in mind.

## Setting the environment for an instance

A `use` block can likewise set `env` (see [blueprint.md](blueprint.md#extra-environment-variables-per-node-env)), which contributes default environment variables to every node this instance expands to:

```hcl
use "eks-service" {
  as     = "checkout"
  source = "./groups/eks-service"
  env = {
    AWS_PROFILE = "prod"
  }
}
```

Unlike `runtime`, this merges rather than replaces: an internal node that sets its own `env` only overrides the specific keys it names, still inheriting anything else the instance's `env` contributed. Nesting works the same way `runtime` does, layer by layer: an inner `use` block's own `env` merges over whatever it inherited from an outer one before passing the result down further.

See it end to end in [`examples/group`](../examples/group).
