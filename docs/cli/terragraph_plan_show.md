## terragraph plan show

Show an execution's recorded phases without rerunning it

```
terragraph plan show <execution-id> [flags]
```

### Options

```
      --backup          write the native state backup to stdout; may contain secrets
  -h, --help            help for show
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
