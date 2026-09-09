# group

A `vpc` node feeding a reusable `eks-service` group (`cluster` + `nodegroup`) instantiated twice (`checkout` and `payments`), proving group expansion, export wiring, the group's own internal edge, per-instance `use.vars` (`cluster_name`), and per-instance Terraform state isolation. `vpc_id` still comes from an edge. See [docs/groups.md](../../docs/groups.md) for the concept. Cloud-credential-free (`random`/`local` providers only).

```
cd examples/group
go run ../../cmd/terragraph graph
# level 1: vpc
# level 2: checkout.cluster, payments.cluster
# level 3: checkout.nodegroup, payments.nodegroup
go run ../../cmd/terragraph apply --auto-approve
ls modules/nodegroup/*.txt   # one file per instance, named after that instance's cluster_id
```

## Keeping backend configuration DRY

For an S3-backed adaptation, declare shared backend settings once per group instance and let terragraph derive a distinct key for each leaf. This keeps bucket and region values out of the reusable group's nodes and removes the need to write a key for every new leaf.

Each module must first declare `backend "s3" {}` inside its own `terraform` block. Terragraph injects the resolved values through `terraform init -backend-config` options; it does not generate or edit the modules' backend blocks. The checked-in example uses local state and needs no AWS credentials, so the following is an S3 adaptation, not part of its default run. The excerpts focus on backend settings; retain the example's existing variables, edges, and exports.

**Before: repeat settings on each leaf.** Inside `group "eks-service"`, configuring the `checkout` instance explicitly would repeat bucket and region, with a different key for each node:

```hcl
node "cluster" {
  source = "../../modules/cluster"
  backend_config = {
    bucket = "example-state"
    region = "ap-northeast-2"
    key    = "prod/checkout.cluster/terraform.tfstate"
  }
}
node "nodegroup" {
  source = "../../modules/nodegroup"
  backend_config = {
    bucket = "example-state"
    region = "ap-northeast-2"
    key    = "prod/checkout.nodegroup/terraform.tfstate"
  }
}
```

Those fixed keys also tie the reusable group to `checkout`; a second instance needs different addresses.

**After: supply shared settings at the instance.** The group's nodes keep only their sources and normal inputs, as in the checked-in group:

```hcl
node "cluster" { source = "../../modules/cluster" }
node "nodegroup" { source = "../../modules/nodegroup" }
```

Configure the backend once on the `use` in `blueprint.hcl`:

```hcl
use "eks-service" {
  as     = "checkout"
  source = "./groups/eks-service"
  vars   = { cluster_name = "checkout" }
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

The generated keys match the explicit `checkout` addresses above. Put the same shared settings on the existing `payments` use and its qualified leaf names automatically produce separate keys:

| Leaf | Generated S3 key |
|---|---|
| `checkout.cluster` | `prod/checkout.cluster/terraform.tfstate` |
| `checkout.nodegroup` | `prod/checkout.nodegroup/terraform.tfstate` |
| `payments.cluster` | `prod/payments.cluster/terraform.tfstate` |
| `payments.nodegroup` | `prod/payments.nodegroup/terraform.tfstate` |

Adding another S3-backed leaf to the group requires no additional bucket/region map or hand-written key. A leaf can set `backend_address = { s3_key_name = "state.json" }` while inheriting the prefix, or preserve an existing location with `backend_config = { key = "legacy/cluster.tfstate" }`. Explicit keys win and still need to be distinct across instances sharing the same state namespace. Use `backend_address = {}` to stop inherited generation for a scope.

Shared `use.backend_config` settings also work with other compatible backend types; automatic key generation currently supports S3 only. S3 generation fields have no effect on this example's local backends. Moving existing local state to S3, renaming an instance, or changing the generated address requires a separate migration decision. See [shared backend configuration](../../docs/blueprint.md#keeping-backend-configuration-dry) for inheritance and address precedence.
