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
