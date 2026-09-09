## terragraph plan prune

Remove eligible completed artifacts and expired terminal records

```
terragraph plan prune [flags]
```

### Options

```
  -h, --help            help for prune
      --output string   output format: text or json (default "text")
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph plan](terragraph_plan.md)	 - Review node plans, actions, approval policy, and evidence limitations
