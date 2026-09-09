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

For an S3-backed version of this group, after its modules declare `backend "s3"`, each `use` can share a bucket while generating separate leaf keys:

```hcl
backend_config = {
  bucket = "example-state"
  region = "ap-northeast-2"
}
backend_address = {
  s3_key_prefix = "prod"
  s3_key_name   = "terraform.tfstate"
}
```

Place these attributes inside both `use` blocks. The cluster keys become `prod/checkout.cluster/terraform.tfstate` and `prod/payments.cluster/terraform.tfstate`; nodegroup keys use their corresponding qualified leaf directories. A group node can set `backend_address = { s3_key_name = "state.json" }` while inheriting the prefix, or keep an existing location with `backend_config = { key = "legacy/cluster.tfstate" }`. Explicit keys still need to be distinct across instances sharing the same state namespace.

The checked-in example continues to use local state and needs no AWS credentials. S3 generation fields have no effect on local backends. Renaming an instance or changing the generation fields can change S3 state locations; terragraph does not migrate them automatically.
