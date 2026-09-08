## terragraph plan

Review node plans, actions, approval policy, and evidence limitations

```
terragraph plan [flags]
```

### Options

```
      --approve string          default policy to assess: none, safe, or all (does not authorize apply) (default "safe")
  -h, --help                    help for plan
      --include-dependencies    include all ancestors of explicitly selected nodes
      --include-dependents      include all descendants of explicitly selected nodes
      --keep-going              continue independent branches after failure; failed descendants remain blocked (default true)
      --node strings            select leaf nodes (repeat or comma-separate); omitted selects the whole graph
      --node-timeout duration   timeout for each entire node action, for example 15m (0 disables)
      --output string           output format: text or json (default "text")
      --output-retries int      additional attempts for failed output queries, 0 through 10; never retries apply or destroy
      --parallelism int         maximum ready nodes to run concurrently (default 1)
      --pool stringArray        shared concurrency limit as name=limit:node,node (repeatable; a node may use several pools)
      --preview                 show execution scope and prerequisites without running Terraform or taking execution locks
      --record-run              checkpoint node statuses under .terragraph/runs for a later --resume
      --resume                  retry unfinished nodes from the last recorded run of this command; plan/apply also recheck ancestors
      --timeout stringArray     override one node timeout as node=duration (repeatable)
```

### Options inherited from parent commands

```
      --blueprint string   path to the blueprint file, or a directory whose .hcl files are merged into one blueprint (default "blueprint.hcl")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
