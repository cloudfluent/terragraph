## terragraph graph

Print execution dependencies or architectural relationships

```
terragraph graph [flags]
```

### Options

```
      --format string   output format: list or dot (default "list")
  -h, --help            help for graph
      --output string   output stream encoding: text or json (json is only supported with --format list) (default "text")
      --view string     graph view: execution or relationships (default "execution")
```

### Options inherited from parent commands

```
      --blueprint string   path to the blueprint file, or a directory whose .hcl files are merged into one blueprint (default "blueprint.hcl")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
