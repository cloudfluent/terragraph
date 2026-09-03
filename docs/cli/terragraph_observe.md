## terragraph observe

Record port evidence (declared/observed/unknown) into the committed terragraph.lock

```
terragraph observe [flags]
```

### Options

```
  -h, --help            help for observe
      --output string   output format: text or json (default "text")
```

### Options inherited from parent commands

```
      --blueprint string   path to the blueprint file, or a directory whose .hcl files are merged into one blueprint (default "blueprint.hcl")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
