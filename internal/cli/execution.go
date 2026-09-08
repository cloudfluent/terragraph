package cli

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/spf13/cobra"
)

// executionFlags is shared by preview and execution so their target and resource constraints cannot drift apart.
type executionFlags struct {
	opts     engine.Options
	preview  bool
	timeouts []string
	pools    []string
}

func (f *executionFlags) bind(cmd *cobra.Command, operation string) {
	flags := cmd.Flags()
	flags.StringSliceVar(&f.opts.Nodes, "node", nil, "select leaf nodes (repeat or comma-separate); omitted selects the whole graph")
	flags.BoolVar(&f.opts.IncludeDependencies, "include-dependencies", false, "include all ancestors of explicitly selected nodes")
	flags.BoolVar(&f.opts.IncludeDependents, "include-dependents", false, "include all descendants of explicitly selected nodes")
	flags.BoolVar(&f.preview, "preview", false, "show execution scope and prerequisites without running Terraform or taking execution locks")
	flags.IntVar(&f.opts.Parallelism, "parallelism", 1, "maximum ready nodes to run concurrently")
	flags.BoolVar(&f.opts.KeepGoing, "keep-going", operation == "plan", "continue independent branches after failure; failed descendants remain blocked")
	flags.DurationVar(&f.opts.NodeTimeout, "node-timeout", 0, "timeout for each entire node action, for example 15m (0 disables)")
	flags.StringArrayVar(&f.timeouts, "timeout", nil, "override one node timeout as node=duration (repeatable)")
	flags.IntVar(&f.opts.OutputRetries, "output-retries", 0, "additional attempts for failed output queries, 0 through 10; never retries apply or destroy")
	flags.StringArrayVar(&f.pools, "pool", nil, "shared concurrency limit as name=limit:node,node (repeatable; a node may use several pools)")
	flags.BoolVar(&f.opts.RecordRun, "record-run", false, "checkpoint node statuses under .terragraph/runs for a later --resume")
	flags.BoolVar(&f.opts.Resume, "resume", false, "retry unfinished nodes from the last recorded run of this command; plan/apply also recheck ancestors")
	if operation == "destroy" {
		flags.BoolVar(&f.opts.AllowOrphanDestroy, "allow-orphan-destroy", false, "acknowledge dependents excluded from this destroy; does not select or destroy them")
	}
}

func (f *executionFlags) options(cmd *cobra.Command) (engine.Options, error) {
	opts := f.opts
	if cmd.Flags().Changed("node") && len(opts.Nodes) == 0 {
		return opts, fmt.Errorf("--node must name at least one leaf; omit the flag to select the whole graph")
	}
	opts.FailFast = !opts.KeepGoing
	opts.Timeouts = map[string]time.Duration{}
	for _, spec := range f.timeouts {
		name, raw, ok := strings.Cut(spec, "=")
		if _, duplicate := opts.Timeouts[name]; !ok || name == "" || duplicate {
			return opts, fmt.Errorf("--timeout needs a unique node=duration entry")
		}
		duration, err := time.ParseDuration(raw)
		if err != nil || duration < 0 {
			return opts, fmt.Errorf("--timeout %q: use a non-negative duration such as 15m", spec)
		}
		opts.Timeouts[name] = duration
	}
	opts.Pools = nil
	for _, spec := range f.pools {
		name, raw, ok := strings.Cut(spec, "=")
		limitText, nodes, found := strings.Cut(raw, ":")
		limit, err := strconv.Atoi(limitText)
		if !ok || !found || name == "" || nodes == "" || err != nil || limit < 1 {
			return opts, fmt.Errorf("--pool %q: use name=positive-limit:node,node", spec)
		}
		opts.Pools = append(opts.Pools, engine.ConcurrencyPool{Name: name, Limit: limit, Nodes: strings.Split(nodes, ",")})
	}
	if opts.NodeTimeout < 0 || opts.OutputRetries < 0 || opts.OutputRetries > 10 {
		return opts, fmt.Errorf("execution: use a non-negative node timeout and 0 through 10 output retries")
	}
	if opts.Resume && len(opts.Nodes) > 0 {
		return opts, fmt.Errorf("--resume cannot be combined with --node; use --preview to inspect unfinished targets")
	}
	return opts, nil
}

type executionNodeDTO struct {
	Node                  string          `json:"node"`
	Level                 int             `json:"level"`
	Reason                string          `json:"reason"`
	Prerequisites         []string        `json:"prerequisites"`
	ExternalPrerequisites []string        `json:"external_prerequisites"`
	ExternalInputs        []inputBasisDTO `json:"external_inputs"`
	Pools                 []string        `json:"pools"`
}
type executionPreviewDTO struct {
	SchemaVersion        int                `json:"schema_version"`
	Kind                 string             `json:"kind"`
	Operation            string             `json:"operation"`
	Nodes                []executionNodeDTO `json:"nodes"`
	OutsideDependents    []string           `json:"outside_dependents"`
	DestroyScopeComplete bool               `json:"destroy_scope_complete"`
}

func previewRun(cmd *cobra.Command, blueprintPath *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger, opts engine.Options, operation, output string) error {
	e, err := loadEngine(cmd, blueprintPath, binaryOf, loggerOf)
	if err != nil {
		return err
	}
	if err := checkValidate(cmd, e); err != nil {
		return err
	}
	preview, err := e.Preview(opts, operation)
	if err != nil {
		return err
	}
	dto := executionPreviewDTO{SchemaVersion: 1, Kind: "execution_scope", Operation: operation, Nodes: []executionNodeDTO{}, OutsideDependents: preview.OutsideDependents, DestroyScopeComplete: preview.DestroyScopeComplete}
	for _, item := range preview.Nodes {
		node := executionNodeDTO{Node: item.Node, Level: item.Level, Reason: item.Reason, Prerequisites: item.Prerequisites, ExternalPrerequisites: item.ExternalPrerequisites, ExternalInputs: []inputBasisDTO{}, Pools: item.Pools}
		for _, input := range item.ExternalInputs {
			node.ExternalInputs = append(node.ExternalInputs, inputBasisDTO{Input: input.Input, Node: input.Node, Output: input.Output, Source: input.Source})
		}
		dto.Nodes = append(dto.Nodes, node)
	}
	if output == "json" {
		return writeJSON(cmd.OutOrStdout(), dto)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s execution scope (no Terraform plan or state read)\n", operation)
	for _, node := range dto.Nodes {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: level %d, %s; waits for [%s]\n", node.Node, node.Level, node.Reason, strings.Join(node.Prerequisites, ", "))
		if len(node.ExternalPrerequisites) > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  outside prerequisites: %s\n", strings.Join(node.ExternalPrerequisites, ", "))
		}
		for _, input := range node.ExternalInputs {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  input %s reads existing node.%s.output.%s\n", input.Input, input.Node, input.Output)
		}
		if len(node.Pools) > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  shared pools: %s\n", strings.Join(node.Pools, ", "))
		}
	}
	if !dto.DestroyScopeComplete {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "destroy requires --include-dependents or --allow-orphan-destroy; outside dependents: %s\n", strings.Join(dto.OutsideDependents, ", "))
	}
	return nil
}
