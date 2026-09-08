# Groups: reusable sub-blueprints

Use a group when several modules belong together, such as a cluster and its node group. A `group` defines their nodes, internal edges, and public ports once; a `use` creates an instance under a new name.

This example assumes the cluster module declares `vpc_id` and `cluster_name` inputs and a `cluster_id` output, and the node group module declares a `cluster_id` input. These modules are available in [`examples/group`](../examples/group).

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
    input "vpc_id"       { to = node.cluster.input.vpc_id }
    input "cluster_name" { to = node.cluster.input.cluster_name }
    output "cluster_id"  { from = node.cluster.output.cluster_id }
  }
}
```

```hcl
# blueprint.hcl
node "vpc" { source = "./modules/vpc" }

use "eks-service" {
  as     = "checkout"
  source = "./groups/eks-service"
  vars = {
    cluster_name = "checkout"
  }
}

edge {
  from = node.vpc.output.vpc_id
  to   = use.checkout.input.vpc_id
}
```

The instance expands in memory into `checkout.cluster` and `checkout.nodegroup`; no configuration files are generated. Commands use these qualified leaf names, for example `terragraph plan --node checkout.cluster`. Selecting that leaf does not select the whole group or its dependencies. A downstream module can consume the public output through `use.checkout.output.cluster_id`.

Add another `use "eks-service"` with a different `as` and `vars` to deploy the same combination again. The [complete example](../examples/group) instantiates it as both `checkout` and `payments`.

## Exported ports and edges

Only ports declared in `export` are available to external edges. `use.checkout.output.cluster_id` is valid; `use.checkout.cluster.output.cluster_id` is not. Add an export when consumers need another input or output. Terragraph checks every exported port against the internal module's declarations.

An exported input may supply several internal inputs:

```hcl
export {
  input "region" {
    to = [node.a.input.region, node.b.input.region]
  }
}
```

This fan-out requires both internal nodes to declare that variable. An exported output always forwards one internal output. After expansion, each leaf input still permits only one source: multiple data edges, or a data edge combined with `vars`, are errors even when the repeated values came through different exports.

Ordering-only edges need no exported ports. `from = node.vpc` and `to = use.checkout` makes every root of `checkout` wait for `vpc`; `from = use.checkout` waits for every sink before starting the destination. Between two groups, every downstream root waits for every upstream sink. Roots have no internal predecessors, and sinks have no internal successors.

Edges involving groups also support [nested `input` blocks](blueprint.md#several-values-between-the-same-two-nodes-input). Each block wires a value through the relevant export just like an individual data edge.

Groups can contain nested `use` blocks, and exports can forward nested ports such as `use.inner.output.cluster_id`. A group that directly or indirectly uses itself is rejected.

## Source directories and filenames

`use.source` names a **local directory**, not an individual file or a remote module address. It is resolved from the directory containing the calling blueprint or group definition. Local internal node sources and nested `use` sources are relative to the group's own source directory. Remote sources are supported for the group's nodes through [vendoring](#vendoring-group-nodes).

Terragraph reads `.hcl` files directly inside the group directory, excluding `.terraform.lock.hcl` and without recursion, then selects the `group` whose label matches the `use` label. In the example, it looks for `group "eks-service"` anywhere in `./groups/eks-service`. `group.hcl` is a convention: renaming it to `components.hcl` needs no change to the `use` block, and the directory name need not match the group name.

All files use the same syntax rules. A group's nodes, edges, nested uses, and `export` must be inside its body to belong to it; top-level nodes in neighboring files are not included automatically. A `runtime` declaration belongs outside the group body, in the same file or another `.hcl` file in that directory. Defining the same `group` name in two files is rejected rather than merging their bodies.

The calling blueprint can also span arbitrary filenames such as `nodes.hcl` and `edges.hcl`; commands include both by default when run in that directory. See [files and loading](blueprint.md#files-and-loading) for a complete split example. The calling blueprint can select one file with `--blueprint <file>`; group sources always use directory loading with the same filename filter.

## Setting literal inputs for an instance

`use.vars` sets the group's exported inputs. For example, the `cluster_name = "checkout"` above reaches `node.cluster.input.cluster_name` through the declared export. A second instance can supply another name:

```hcl
use "eks-service" {
  as     = "payments"
  source = "./groups/eks-service"
  vars = {
    cluster_name = "payments"
  }
}

edge {
  from = node.vpc.output.vpc_id
  to   = use.payments.input.vpc_id
}
```

Keys are **export input names**, not internal node paths. As with [`node.vars`](blueprint.md#literal-input-values-vars), values are literal data with no variable references or functions; another node's output needs an edge.

The value reaches every leaf named by the export, including through nested groups. An unknown export name is an error. A leaf input already set by a data edge, internal `node.vars`, or another `use.vars` is also an error; instance vars do not override existing values. They fill the exported inputs rather than applying to every node automatically.

## Isolating state for an instance

For local state, the group's modules can declare `backend "local" {}`. When no explicit `backend_config.path` is set, terragraph supplies a unique default-workspace path per qualified leaf, such as `.terragraph/state/checkout.cluster.tfstate` and `.terragraph/state/payments.cluster.tfstate`, under the root blueprint directory. See [module reuse](blueprint.md#reusing-the-same-module-across-instances) for explicit paths, workspaces, and migration considerations.

For remote state, `use.backend_config` supplies defaults to every node in the instance:

```hcl
use "eks-service" {
  as     = "checkout"
  source = "./groups/eks-service"
  backend_config = {
    bucket  = "tfstate"
    profile = "prod"
    region  = "ap-northeast-2"
  }
}
```

Each module must declare a compatible backend. An inner `use` overrides inherited keys, and a node's own keys win. Use shared fields such as `bucket`, `profile`, and `region` here, and give each leaf a distinct state address. A single `key` on `use` is inherited by every leaf; terragraph does not interpolate the instance or leaf name into it. When reusing the group, also separate the instances' backend namespaces, for example with different buckets.

## Choosing a runtime for an instance

`use.runtime` selects the default binary for the instance. Declare the referenced runtime in the calling scope:

```hcl
runtime "tofu" {
  binary = "tofu"
}

use "eks-service" {
  as      = "checkout"
  source  = "./groups/eks-service"
  runtime = runtime.tofu
}
```

A node's own runtime or a nearer nested `use.runtime` takes precedence. The group's source directory may declare runtimes for its internal nodes and uses, but marking one `default = true` does not make it the instance's default. See [runtime selection](blueprint.md#choosing-a-runtime-per-node-runtime) for the full fallback order and Terraform/OpenTofu compatibility.

## Setting the environment for an instance

`use.env` supplies environment defaults to every node in the instance:

```hcl
use "eks-service" {
  as     = "checkout"
  source = "./groups/eks-service"
  env = {
    AWS_PROFILE = "prod"
  }
}
```

Environment maps merge: a nested `use` overrides inherited keys, and each node overrides only the keys it sets. All remaining inherited entries stay in place. `TF_DATA_DIR` is reserved and cannot appear in any node or `use` environment, regardless of case or an empty value. See the [environment rules](blueprint.md#extra-environment-variables-per-node-env).

## Setting an approval policy for an instance

Use `approve` to set a resource-change policy for the instance:

```hcl
use "eks-service" {
  as      = "checkout"
  source  = "./groups/eks-service"
  approve = "safe"
}
```

This allows create and update actions by default for the instance's nodes. A node's own `approve` or a nearer nested `use.approve` takes precedence. The CLI's `--approve` is only a fallback and cannot override a declared policy. `--auto-approve` controls confirmation prompts separately. See [allowed changes and execution behavior](execution-model.md#what-a-node-may-do-approve) for `none`, `safe`, and `all`.

## Vendoring group nodes

`terragraph vendor` fetches remote node sources inside local groups into the calling blueprint's vendor directory under their qualified names, such as `vendor/checkout.vpc/` or `vendor/prod.inner.vpc/`. Run `terragraph vendor --node prod.inner.vpc` to fetch one leaf. Each instance gets its own copy; the group's source directory is not modified.

The root blueprint's `vendor.directory` and `vendor.manifest_file` control these copies and their manifest. Existing group-local copies remain usable when no root qualified copy exists. Refreshes publish into the root vendor directory and check local state before changing execution directories; see [vendoring](vendoring.md) for compatibility and refresh rules.
