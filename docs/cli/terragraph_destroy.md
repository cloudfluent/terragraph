## terragraph destroy

Run terraform/tofu destroy across the graph in reverse dependency order

```
terragraph destroy [flags]
```

### Options

```
      --auto-approve            skip interactive approval
      --downstream              include all successors of --node across data and ordering edges
  -h, --help                    help for destroy
      --node stringArray        select an exact leaf name (repeat for multiple nodes; commas are literal)
      --node-timeout duration   deadline per node action, excluding queue time (0 disables; apply/destroy require --auto-approve)
      --output string           output format: text or json (default "text")
      --parallelism int         max nodes to run concurrently within one execution level (default 1)
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
