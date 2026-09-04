## terragraph force-unlock

Release a leftover graph lock object left by an interrupted run

```
terragraph force-unlock [flags]
```

### Options

```
  -h, --help   help for force-unlock
      --yes    release the lock object (required; the lock may still be genuinely held)
```

### Options inherited from parent commands

```
      --blueprint string   path to the blueprint file, or a directory whose .hcl files are merged into one blueprint (default "blueprint.hcl")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
