## terragraph destroy

Run terraform/tofu destroy across the graph in reverse dependency order

```
terragraph destroy [flags]
```

### Options

```
      --approve string    what a node may do without saying so per run: none, safe (create/update), or all (adds replace/delete); a node's own approve wins over this (default "safe")
      --auto-approve      skip interactive approval
  -h, --help              help for destroy
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
