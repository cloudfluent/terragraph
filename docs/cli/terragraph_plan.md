## terragraph plan

Run terraform/tofu plan across the graph in dependency order

```
terragraph plan [flags]
```

### Options

```
  -h, --help              help for plan
      --node string       restrict to a single node
      --parallelism int   max nodes to run concurrently within one execution level (default 1)
```

### Options inherited from parent commands

```
      --blueprint string   path to the blueprint file, or a directory whose .hcl files are merged into one blueprint (default "blueprint.hcl")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
