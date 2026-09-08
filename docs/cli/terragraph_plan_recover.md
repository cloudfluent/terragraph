## terragraph plan recover

Recover outputs or retire an inspected uncertain attempt without replaying it

```
terragraph plan recover <execution-id> [flags]
```

### Options

```
      --confirm-stopped      confirm the previous executor has stopped; does not force-unlock anything
  -h, --help                 help for recover
      --initialize-backend   allow journaled backend initialization for recovery when read-only preparation is unsupported
      --output string        output format: text or json (default "text")
      --replan               retire the attempt while preserving its recorded outcome; requires a fresh plan
      --state-reviewed       confirm actual state and affected resources have been inspected
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph plan](terragraph_plan.md)	 - Review node plans, actions, approval policy, and evidence limitations
