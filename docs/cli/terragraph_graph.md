## terragraph graph

Print the resolved execution levels or a Graphviz DOT rendering

```
terragraph graph [flags]
```

### Options

```
      --format string   output format: list or dot (default "list")
  -h, --help            help for graph
      --output string   output stream encoding: text or json (json is only supported with --format list) (default "text")
```

### Options inherited from parent commands

```
      --blueprint string   path to the blueprint file (default "blueprint.hcl")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
