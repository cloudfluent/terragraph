## terragraph graph

Print the resolved execution levels, a Graphviz DOT rendering, or detailed node inspection

```
terragraph graph [flags]
```

### Options

```
      --approve string     approve policy to explain with --detail: none, safe, or all (resolves like apply's default filling; never executes or authorizes anything) (default "safe")
      --detail             print detailed node inspection with wiring provenance, effective settings, and review scope instead of execution levels
      --downstream         include all successors of --node across data and ordering edges
      --format string      output format: list or dot (dot cannot be combined with --detail) (default "list")
  -h, --help               help for graph
      --node stringArray   select an exact leaf name (repeat for multiple nodes; commas are literal)
      --output string      output stream encoding: text or json (json is only supported with --format list) (default "text")
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
