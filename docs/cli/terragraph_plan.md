## terragraph plan

Review node plans, actions, approval policy, and evidence limitations

```
terragraph plan [flags]
```

### Options

```
      --approve string          default policy to assess: none, safe, or all (does not authorize apply) (default "safe")
      --continue string         create the next frontier after applying this saved execution
      --downstream              include all successors of --node across data and ordering edges
  -h, --help                    help for plan
      --node stringArray        select an exact leaf name (repeat for multiple nodes; commas are literal)
      --node-timeout duration   deadline per node action, excluding queue time (0 disables; apply/destroy require --auto-approve)
      --output string           output format: text or json (default "text")
      --parallelism int         max nodes to run concurrently within one execution level (default 1)
      --save                    save only the ready graph frontier for a later apply --plan
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
* [terragraph plan cancel](terragraph_plan_cancel.md)	 - Cancel a paused execution without touching infrastructure
* [terragraph plan list](terragraph_plan_list.md)	 - List execution records without reading infrastructure state
* [terragraph plan prune](terragraph_plan_prune.md)	 - Remove eligible completed artifacts and expired terminal records
* [terragraph plan recover](terragraph_plan_recover.md)	 - Recover outputs or retire an inspected uncertain attempt without replaying it
* [terragraph plan show](terragraph_plan_show.md)	 - Show an execution's recorded phases without rerunning it
