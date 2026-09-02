# reuse

The same module (`stacks/vpc`, with an empty `backend "local" {}`) instantiated twice (`vpc_a`, `vpc_b`) via distinct `backend_config.path` values, proving state stays isolated per instance. Cloud-credential-free (`random`/`local` providers only).

```
cd examples/reuse
go run ../../cmd/terragraph apply --auto-approve
# stacks/vpc/.terragraph/state/vpc_a.tfstate and vpc_b.tfstate hold different vpc_id values
```
