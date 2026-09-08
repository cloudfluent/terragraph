## terragraph plan

Review node plans, actions, approval policy, and evidence limitations

```
terragraph plan [flags]
```

### Options

```
      --approve string    default policy to assess: none, safe, or all (does not authorize apply) (default "safe")
  -h, --help              help for plan
      --node string       restrict to a single node
      --output string     output format: text or json (default "text")
      --parallelism int   max nodes to run concurrently within one execution level (default 1)
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
