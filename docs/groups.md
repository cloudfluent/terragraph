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

This expands purely in memory when the graph is built (no files are generated) into real nodes namespaced under the instance name (`checkout.cluster`, `checkout.nodegroup`), which every command (`plan`, `apply`, `--node`, the incremental-apply cache, `graph`) treats exactly like any other node.

A few rules fall out of that expansion:
- **Only `export`-declared ports are visible from outside the instance.** `use.checkout.output.cluster_id` works; `use.checkout.cluster.output.cluster_id` doesn't even parse. This is deliberate: a group is only safely reusable if its author can change internals without an unbounded set of external edges depending on them, the same reason Terraform modules, Go's unexported identifiers, and private class members all work this way. If a consumer needs something not currently exported, the fix is adding it to the group's `export` block, not reaching around it. Plain nodes (not inside a group) have no such restriction.
- **A data edge into a group's exposed input can fan out** (`to = [node.a.input.x, node.b.input.x]`) because "which internal nodes need this value" is a fact about the group's own design that its author must state; it isn't inferable from graph structure. An exposed output is always a 1:1 passthrough, so it never needs this.
- **A bare, ordering-only edge into or out of a group needs no such declaration**: `edge { from = node.x, to = use.checkout }` (no port) expands automatically to every node inside the group with no internal predecessor (its "roots"); the symmetric case on the `from` side expands to every node with no internal successor (its "sinks"). Unlike fan-out, this is inferable directly from the group's internal graph shape.
- **`node`↔`group` and `group`↔`group` edges use identical syntax** to `node`↔`node`. A group instance is indistinguishable from a node both in edge references and in schema: internal nodes are inspected exactly as today (`module.Inspect` against their real `.tf` files), the `export` block is validated against those real schemas, and once valid it's synthesized into the same schema shape a real module has. Groups can nest (a group's own `use` blocks resolve recursively), and a group that directly or transitively uses itself is a validation error.

See it end to end in [`examples/group`](../examples/group).
