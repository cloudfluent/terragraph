## terragraph apply

Run terraform/tofu apply across the graph in dependency order, wiring outputs to inputs

```
terragraph apply [flags]
```

### Options

```
      --auto-approve      skip interactive approval
      --force             bypass the incremental-apply cache and always re-run apply
  -h, --help              help for apply
      --node string       restrict to a single node
      --parallelism int   max nodes to run concurrently within one execution level (default 1)
```

### Options inherited from parent commands

```
      --blueprint string   path to the blueprint file (default "blueprint.hcl")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
