## terragraph apply

Run terraform/tofu apply across the graph in dependency order, wiring outputs to inputs

```
terragraph apply [flags]
```

### Options

```
      --approve string    what a node may do without saying so per run: none, safe (create/update), or all (adds replace/delete); a node's own approve wins over this (default "safe")
      --auto-approve      skip the interactive approval prompt
  -h, --help              help for apply
      --node string       restrict to a single node
      --output string     output format: text or json (default "text")
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
