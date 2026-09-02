## terragraph vendor

Fetch remote node sources into a local, committable directory

```
terragraph vendor [flags]
```

### Options

```
      --force           re-fetch even if already vendored
  -h, --help            help for vendor
      --node string     restrict to a single node
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
