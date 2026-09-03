# group

A `vpc` node feeding a reusable `eks-service` group (`cluster` + `nodegroup`) instantiated as `checkout`, proving group expansion, export wiring, and the group's own internal edge all work end to end. `cluster_name` is instance data via `use.vars`; `vpc_id` still comes from an edge. See [docs/groups.md](../../docs/groups.md) for the concept. Cloud-credential-free (`random`/`local` providers only).

```
cd examples/group
go run ../../cmd/terragraph graph
# level 1: vpc
# level 2: checkout.cluster
# level 3: checkout.nodegroup
go run ../../cmd/terragraph apply --auto-approve
cat modules/nodegroup/cluster_id.txt   # matches checkout.cluster's cluster_id output
```
