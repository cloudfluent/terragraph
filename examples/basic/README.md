# basic

One node (`vpc`) feeding two independent downstream nodes (`eks`, `eks2`), proving the wiring, parallel level execution, and that an unchanged node is skipped. Cloud-credential-free (`random`/`local` providers only).

```
cd examples/basic
go run ../../cmd/terragraph apply --parallelism 2 --auto-approve
cat stacks/eks/vpc_id.txt stacks/eks2/vpc_id.txt   # both match stacks/vpc's vpc_id output
go run ../../cmd/terragraph apply --parallelism 2 --auto-approve   # re-run: all 3 nodes "unchanged, skipping apply"
```
