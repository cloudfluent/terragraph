// Package cli wires together terragraph's cobra command tree. It is separate from cmd/terragraph so tools/gendocs can import NewRootCmd and generate docs/cli/*.md directly from the same Use/Short/flag definitions the actual binary runs. The CLI reference can't drift from the real commands because it's generated from them, not hand-maintained alongside them.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/cloudfluent/terragraph/internal/graphlock"
	"github.com/cloudfluent/terragraph/internal/lsp"
	"github.com/cloudfluent/terragraph/internal/plugins"
	"github.com/cloudfluent/terragraph/internal/runlock"
	"github.com/cloudfluent/terragraph/internal/vendor"
)

// NewRootCmd builds the terragraph command tree. version is surfaced via cobra's built-in --version flag; callers that don't care what it prints (tools/gendocs, tests) can pass any non-empty placeholder.
func NewRootCmd(version string) *cobra.Command {
	var (
		blueprintPath string
		useTofu       bool
		logLevel      string
		logger        *slog.Logger
	)

	binaryOf := func() exec.Binary {
		if useTofu {
			return exec.OpenTofu
		}
		return exec.Terraform
	}
	// loggerOf is resolved lazily (not captured by value) because it's read from subcommand RunE closures, which run after PersistentPreRunE has populated logger.
	loggerOf := func() *slog.Logger { return logger }

	root := &cobra.Command{
		Use:           "terragraph",
		Short:         "Graph-based orchestration for independent Terraform/OpenTofu root modules",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			level, err := parseLogLevel(logLevel)
			if err != nil {
				return err
			}
			logger = newLogger(cmd.ErrOrStderr(), level)
			cmd.SetContext(plugins.WithLogger(cmd.Context(), logger))
			return nil
		},
	}
	root.PersistentFlags().StringVar(&blueprintPath, "blueprint", ".", "path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl")
	root.PersistentFlags().BoolVar(&useTofu, "tofu", false, "use the tofu binary instead of terraform")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "warn", "log verbosity for internal diagnostics on stderr: debug, info, warn, or error")

	root.AddCommand(newValidateCmd(&blueprintPath, binaryOf, loggerOf))
	root.AddCommand(newGraphCmd(&blueprintPath, binaryOf, loggerOf))
	root.AddCommand(newObservationCmd("output", &blueprintPath, binaryOf))
	root.AddCommand(newObservationCmd("status", &blueprintPath, binaryOf))
	root.AddCommand(newPlanCmd(&blueprintPath, binaryOf, loggerOf))
	root.AddCommand(newApplyCmd(&blueprintPath, binaryOf, loggerOf))
	root.AddCommand(newNodeOperationCmd(&blueprintPath, binaryOf, loggerOf))
	root.AddCommand(newDestroyCmd(&blueprintPath, binaryOf, loggerOf))
	root.AddCommand(newForceUnlockCmd(&blueprintPath))
	root.AddCommand(newVendorCmd(&blueprintPath, loggerOf))
	root.AddCommand(newLanguageServerCmd())
	root.AddCommand(newPluginCmd(&blueprintPath))

	return root
}

func newLanguageServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "language-server",
		Aliases: []string{"lsp"},
		Short:   "Run the Blueprint language server over standard input/output",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return lsp.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

// loadEngine loads the blueprint into an Engine and wires the CLI's logger into it. Shared by every command that needs a built graph (validate/graph/plan/apply/destroy); vendor parses the blueprint directly instead (see newVendorCmd) since building the graph would fail for any not-yet-vendored remote node.
func loadEngine(cmd *cobra.Command, blueprintPath *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger) (*engine.Engine, error) {
	diagnosticPhase(cmd, "load")
	e, err := engine.LoadContext(plugins.WithLogger(cmd.Context(), loggerOf()), *blueprintPath, binaryOf(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return nil, err
	}
	wireEngine(cmd, e, loggerOf, *blueprintPath)
	return e, nil
}

// loadLockedEngine is loadEngine after taking the blueprint process lock, so plan/apply/destroy inspect module files only once a concurrent vendor cannot rewrite them. The caller must invoke the returned func when the command ends.
func loadLockedEngine(cmd *cobra.Command, blueprintPath *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger) (*engine.Engine, func(), error) {
	diagnosticPhase(cmd, "load")
	e, unlock, err := engine.LoadLockedContext(plugins.WithLogger(cmd.Context(), loggerOf()), *blueprintPath, binaryOf(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return nil, nil, err
	}
	wireEngine(cmd, e, loggerOf, *blueprintPath)
	return e, unlock, nil
}

func wireEngine(cmd *cobra.Command, e *engine.Engine, loggerOf func() *slog.Logger, blueprintPath string) {
	e.Context = cmd.Context()
	e.Stdin = cmd.InOrStdin()
	e.Logger = loggerOf()
	e.Logger.Debug("blueprint loaded", "path", blueprintPath, "nodes", len(e.Graph.Nodes))
}

// checkValidate prints every problem found in the graph (Errors and Warnings alike, so a user sees the whole picture at once) and returns a non-nil error only if at least one is an Error. Warnings never block graph/plan/apply/destroy.
func checkValidate(cmd *cobra.Command, e *engine.Engine) error {
	problems := e.Validate()
	errorCount := 0
	for _, p := range problems {
		label := "ERROR"
		if !p.IsError() {
			label = "WARNING"
		} else {
			errorCount++
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[%s] %s\n", label, p.Message)
	}
	if errorCount > 0 {
		err := fmt.Errorf("blueprint has %d error(s); run \"terragraph validate\" for details", errorCount)
		return &validationError{cause: err, problems: problems}
	}
	return nil
}

// finishRun retains execution identity and independent diagnostics even when no node could start.
func finishRun(cmd *cobra.Command, output string, result engine.RunResult, err error, selection ...*selectionDTO) error {
	var scope *selectionDTO
	if len(selection) > 0 {
		scope = selection[0]
	}
	if output == "json" {
		diagnostics := runDiagnostics(result, err)
		var payload any = runResult{SchemaVersion: 1, ExecutionID: result.ExecutionID, Nodes: nodeRunsToDTO(result.Nodes), Diagnostics: diagnostics, Selection: scope}
		if err != nil && result.ExecutionID == "" && len(result.Nodes) == 0 && scope == nil {
			payload = errorResultDTO{SchemaVersion: 1, Diagnostics: diagnostics}
		}
		if werr := writeJSON(cmd, payload); werr != nil {
			return werr
		}
	}
	return err
}

func newValidateCmd(blueprintPath *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Parse the blueprint and check it against the real module schemas",
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("unknown output %q (want \"text\" or \"json\")", output)
			}

			e, err := loadEngine(cmd, blueprintPath, binaryOf, loggerOf)
			if err != nil {
				return err
			}

			if output == "json" {
				problems := e.Validate()
				errorCount := 0
				for _, p := range problems {
					if p.IsError() {
						errorCount++
					}
				}
				if err := writeJSON(cmd, validateResult{SchemaVersion: 1, Valid: errorCount == 0, Problems: problemsToDTO(problems)}); err != nil {
					return err
				}
				if errorCount > 0 {
					return fmt.Errorf("blueprint has %d error(s)", errorCount)
				}
				return nil
			}

			if err := checkValidate(cmd, e); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "blueprint is valid")
			return nil
		},
	}
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	return cmd
}

func newGraphCmd(blueprintPath *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger) *cobra.Command {
	var selection selectionFlags
	var format string
	var output string
	cmd := &cobra.Command{
		Use:   "graph",
		Args:  validateRunArgs,
		Short: "Print the resolved execution levels or a Graphviz DOT rendering",
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("unknown output %q (want \"text\" or \"json\")", output)
			}
			if output == "json" && format == "dot" {
				return fmt.Errorf("--output json is not supported with --format dot")
			}

			e, err := loadEngine(cmd, blueprintPath, binaryOf, loggerOf)
			if err != nil {
				return err
			}
			if err := checkValidate(cmd, e); err != nil {
				return err
			}

			scope, err := graph.Select(e.Graph, selection.nodes, selection.downstream)
			if err != nil {
				return err
			}
			var names []string
			if scope != nil {
				names = scope.Names()
			}
			levels, err := graph.SelectedLevels(e.Graph, names, false)
			if err != nil {
				return err
			}
			dto := selectionToDTO(scope)
			switch format {
			case "dot":
				_, _ = fmt.Fprint(cmd.OutOrStdout(), graph.SelectionDOT(e.Graph, scope))
			case "list", "":
				if output == "json" {
					return writeJSON(cmd, graphResult{SchemaVersion: 1, Levels: levels, Selection: dto, Diagnostics: problemDiagnostics(e.Validate())})
				}
				printSelection(cmd.OutOrStdout(), dto, nil)
				for i, level := range levels {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "level %d: %s\n", i+1, joinNames(level))
				}
			default:
				return fmt.Errorf("unknown format %q (want \"list\" or \"dot\")", format)
			}
			return nil
		},
	}
	selection.add(cmd)
	cmd.Flags().StringVar(&format, "format", "list", "output format: list or dot")
	cmd.Flags().StringVar(&output, "output", "text", "output stream encoding: text or json (json is only supported with --format list)")
	return cmd
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// validateRunArgs rejects positional targets before loading or locking the graph, since ignoring one would silently execute every node.
func validateRunArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("%s: unexpected arguments %q; use --node <name> to select a single node", cmd.Name(), args)
	}
	return nil
}

func newPlanCmd(blueprintPath *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger) *cobra.Command {
	var selection selectionFlags
	var output, approve string
	var save bool
	var continueID string
	var parallelism int
	var failurePolicy string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Review node plans, actions, approval policy, and evidence limitations",
		RunE: func(cmd *cobra.Command, args []string) (resultErr error) {
			var runs engine.RunResult
			var savedRecord engine.ExecutionRecord
			var scope *selectionDTO
			phase := "arguments"
			defer func() {
				if save {
					resultErr = finishSavedExecution(cmd, output, savedRecord, resultErr, scope)
				} else {
					resultErr = finishPlan(cmd, output, runs, phase, resultErr, scope)
				}
			}()
			if err := validateRunArgs(cmd, args); err != nil {
				return err
			}
			if output != "text" && output != "json" {
				return fmt.Errorf("unknown output %q (want text or json)", output)
			}
			if continueID != "" && !save {
				return fmt.Errorf("--continue requires --save")
			}
			if continueID != "" && selection.specified(cmd) {
				return fmt.Errorf("--continue already fixes node selection; omit --node and --downstream")
			}
			policy, policyErr := blueprint.ParseApprove(approve)
			if policyErr != nil {
				return policyErr
			}
			phase = "load"
			e, unlock, err := loadLockedEngine(cmd, blueprintPath, binaryOf, loggerOf)
			if err != nil {
				return err
			}
			defer unlock()
			phase = "validation"
			if err := checkValidate(cmd, e); err != nil {
				return err
			}
			e.Stdout = cmd.ErrOrStderr()
			phase = "prepare"
			opts := selection.options(cmd, output, &scope)
			opts.FailurePolicy = failurePolicy
			opts.Parallelism, opts.Approve = parallelism, policy
			if save {
				savedRecord, err = e.SavePlans(opts, continueID)
				return err
			}
			runs, err = e.ReviewPlan(opts, output == "text")
			return err
		},
	}
	selection.add(cmd)
	cmd.Flags().StringVar(&failurePolicy, "on-failure", "", "stop queued work or continue independent branches (stop or continue; omitted preserves command default)")
	cmd.Flags().IntVar(&parallelism, "parallelism", 1, "max ready nodes to run concurrently")
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	cmd.Flags().BoolVar(&save, "save", false, "save only the ready graph frontier for a later apply --plan")
	cmd.Flags().StringVar(&continueID, "continue", "", "create the next frontier after applying this saved execution")
	cmd.Flags().StringVar(&approve, "approve", "safe", "default policy to assess: none, safe, or all (does not authorize apply)")
	cmd.AddCommand(newExecutionHistoryCmd("list", blueprintPath), newExecutionHistoryCmd("show", blueprintPath), newExecutionRecoveryCmd(blueprintPath, binaryOf, loggerOf), newExecutionCleanupCmd("cancel", blueprintPath), newExecutionCleanupCmd("prune", blueprintPath))
	return cmd
}

func newApplyCmd(blueprintPath *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger) *cobra.Command {
	var selection selectionFlags
	var planID string
	var retainPlan bool
	var autoApprove bool
	var parallelism int
	var failurePolicy string
	var force bool
	var approve string
	var output string
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Run terraform/tofu apply across the graph in dependency order, wiring outputs to inputs",
		Args:  validateRunArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if planID != "" && selection.specified(cmd) {
				return fmt.Errorf("--plan already fixes node selection; omit --node and --downstream")
			}
			if output != "text" && output != "json" {
				return fmt.Errorf("unknown output %q (want \"text\" or \"json\")", output)
			}
			// Same class as --parallelism's refusal: the approval prompt has nowhere to appear under --output json — stdout is the payload and stderr is diagnostics an automation consumer is not watching for a question — so instead of a prompt nobody answers (or a payload somebody corrupts), the combination is refused up front (#49).
			if output == "json" && !autoApprove {
				return fmt.Errorf("--output json needs --auto-approve: the approval prompt would have nowhere to appear without corrupting the JSON payload; approve in text mode or pass --auto-approve")
			}
			e, unlock, err := loadLockedEngine(cmd, blueprintPath, binaryOf, loggerOf)
			if err != nil {
				return err
			}
			defer unlock()
			if err := checkValidate(cmd, e); err != nil {
				return err
			}
			level, err := blueprint.ParseApprove(approve)
			if err != nil {
				return err
			}
			// Under --output json, terraform's own output is diagnostics, not the result: stdout stays a single JSON document.
			if output == "json" {
				e.Stdout = cmd.ErrOrStderr()
			}
			var scope *selectionDTO
			opts := selection.options(cmd, output, &scope)
			opts.FailurePolicy = failurePolicy
			opts.AutoApprove, opts.Approve, opts.Parallelism, opts.RetainPlan = autoApprove, level, parallelism, retainPlan
			var runs engine.RunResult
			if planID != "" {
				if retainPlan {
					return fmt.Errorf("--plan already uses retained artifacts; omit --retain-plan")
				}
				runs, err = e.ApplySavedPlans(planID, opts)
			} else {
				runs, err = e.Apply(opts)
			}
			return finishRun(cmd, output, runs, err, scope)
		},
	}
	selection.add(cmd)
	cmd.Flags().StringVar(&failurePolicy, "on-failure", "", "stop queued work or continue independent branches (stop or continue; omitted preserves command default)")
	cmd.Flags().StringVar(&planID, "plan", "", "apply the stored frontier of a saved execution without replanning")
	cmd.Flags().BoolVar(&retainPlan, "retain-plan", false, "retain optional plan artifacts while ordinary apply continues")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "skip the interactive approval prompt")
	cmd.Flags().StringVar(&approve, "approve", string(blueprint.ApproveSafe), "what a node may do without saying so per run: none, safe (create/update), or all (adds replace/delete); a node's own approve wins over this")
	cmd.Flags().IntVar(&parallelism, "parallelism", 1, "max ready nodes to run concurrently")
	// Accepted and ignored for one release so existing scripts keep running. There is no longer a local cache to bypass: apply asks Terraform whether each node needs applying, every run.
	cmd.Flags().BoolVar(&force, "force", false, "no longer has any effect")
	_ = cmd.Flags().MarkDeprecated("force", "there is no local cache to bypass; apply now plans every node")
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	return cmd
}

func newDestroyCmd(blueprintPath *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger) *cobra.Command {
	var selection selectionFlags
	var autoApprove bool
	var parallelism int
	var failurePolicy string
	var output string
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Run terraform/tofu destroy across the graph in reverse dependency order",
		Args:  validateRunArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("unknown output %q (want \"text\" or \"json\")", output)
			}
			// Same class as --parallelism's refusal: terraform's destroy confirmation has nowhere to appear under --output json — stdout is the payload and stderr is diagnostics an automation consumer is not watching for a question — so the combination is refused up front (#49).
			if output == "json" && !autoApprove {
				return fmt.Errorf("--output json needs --auto-approve: the destroy confirmation would have nowhere to appear without corrupting the JSON payload; confirm in text mode or pass --auto-approve")
			}
			e, unlock, err := loadLockedEngine(cmd, blueprintPath, binaryOf, loggerOf)
			if err != nil {
				return err
			}
			defer unlock()
			if err := checkValidate(cmd, e); err != nil {
				return err
			}
			// Under --output json, terraform's own output is diagnostics, not the result: stdout stays a single JSON document.
			if output == "json" {
				e.Stdout = cmd.ErrOrStderr()
			}
			var scope *selectionDTO
			opts := selection.options(cmd, output, &scope)
			opts.FailurePolicy = failurePolicy
			opts.AutoApprove, opts.Parallelism = autoApprove, parallelism
			runs, err := e.Destroy(opts)
			return finishRun(cmd, output, runs, err, scope)
		},
	}
	selection.add(cmd)
	cmd.Flags().StringVar(&failurePolicy, "on-failure", "", "stop queued work or continue independent branches (stop or continue; omitted preserves command default)")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "skip interactive approval")
	cmd.Flags().IntVar(&parallelism, "parallelism", 1, "max ready nodes to run concurrently")
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	// No --approve here, unlike apply: destroy's gate reads what a node declared, and the layering rule is that a CLI flag only fills a gap nothing else spoke to — so a flag could never permit a teardown the blueprint refused, and offering one would only suggest otherwise.
	return cmd
}

func newForceUnlockCmd(blueprintPath *string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "force-unlock",
		Short: "Release a leftover graph lock object left by an interrupted run",
		RunE: func(cmd *cobra.Command, args []string) error {
			// The blueprint is parsed, not built into a graph: all this needs is the lock block, and graph.Build stats every node source — so a checkout with nothing vendored yet would abort the one command that exists to recover from an interrupted run. Same reason newVendorCmd parses directly.
			diagnosticPhase(cmd, "load")
			bp, dir, err := blueprint.LoadMetadata(*blueprintPath)
			if err != nil {
				return err
			}
			if bp.Lock == nil {
				return fmt.Errorf("this blueprint declares no graph lock; there is nothing for force-unlock to release")
			}
			baseDir, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("resolving blueprint directory: %w", err)
			}

			// A live process on this checkout is the likeliest legitimate holder of the graph lock about to be deleted, and flock drops with the fd on exit, so a held one means someone is genuinely running rather than that a crash left it behind. It says nothing about other machines — that is what --yes is for — but the same-machine mistake is free to catch.
			lock, err := runlock.TryAcquire(baseDir)
			if err != nil {
				if errors.Is(err, runlock.ErrHeld) {
					return fmt.Errorf("another terragraph process is using this blueprint and may hold the graph lock legitimately; wait for it to finish")
				}
				return fmt.Errorf("locking blueprint: %w", err)
			}
			defer func() { _ = lock.Close() }()

			s3 := bp.Lock.S3
			// Who holds the lock is worth showing but never worth waiting on: this is the command someone reaches for when a backend is already misbehaving, so an unreachable one degrades to "unknown" on a short deadline rather than stalling the refusal it exists to explain.
			holderCtx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			who, created := graphlock.Holder(holderCtx, bp.Lock)
			cancel()
			held := ""
			if who != "" {
				held = fmt.Sprintf(", held by %s since %s", who, created)
			}

			if !yes {
				return fmt.Errorf("refusing to release s3://%s/%s%s without --yes; the holder may still be running, and releasing is unconditional", s3.Bucket, s3.Key, held)
			}
			if err := graphlock.Release(cmd.Context(), bp.Lock); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "released s3://%s/%s%s\n", s3.Bucket, s3.Key, held)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "release the lock object (required; the lock may still be genuinely held)")
	return cmd
}

func newVendorCmd(blueprintPath *string, loggerOf func() *slog.Logger) *cobra.Command {
	var node string
	var force bool
	var output string
	cmd := &cobra.Command{
		Use:   "vendor",
		Short: "Fetch remote node sources into a local, committable directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("unknown output %q (want \"text\" or \"json\")", output)
			}
			logger := loggerOf()

			// Parsed directly, not via engine.Load: building the full graph would fail for any not-yet-vendored remote node, but vendoring has to work *before* the graph is buildable.
			diagnosticPhase(cmd, "load")
			baseDir, err := blueprint.BaseDirectory(*blueprintPath)
			if err != nil {
				return err
			}
			lock, err := runlock.AcquireContext(cmd.Context(), baseDir, cmd.ErrOrStderr())
			if err != nil {
				return fmt.Errorf("locking blueprint: %w", err)
			}
			defer func() { _ = lock.Close() }()
			evaluation, err := plugins.Evaluate(plugins.WithLogger(cmd.Context(), logger), *blueprintPath)
			if err != nil {
				return err
			}
			defer func() {
				if err := evaluation.Close(); err != nil {
					logger.Error("plugin cleanup failed", "error", err)
				}
			}()
			bp, _, err := blueprint.LoadPath(*blueprintPath, evaluation.Context)
			if err != nil {
				return err
			}

			sources, err := graph.SourceNodes(bp, baseDir)
			if err != nil {
				return err
			}
			diagnosticPhase(cmd, "selection")
			var nodes []blueprint.Node
			legacyDirs := make(map[string]string)
			for _, source := range sources {
				if node != "" && source.Name != node {
					continue
				}
				if node != "" && !blueprint.IsRemote(source.Source) {
					return fmt.Errorf("node %q has a local source (%q); nothing to vendor", node, source.Source)
				}
				nodes = append(nodes, source.Node)
				if source.LegacyDir != "" {
					legacyDirs[source.Name] = source.LegacyDir
				}
			}
			if node != "" && len(nodes) == 0 {
				return fmt.Errorf("unknown node %q", node)
			}

			diagnosticPhase(cmd, "artifact")
			results, err := vendor.All(nodes, baseDir, bp.VendorDirectory(), filepath.Join(baseDir, bp.VendorManifestFile()), vendor.Options{Force: force, LegacyDirectories: legacyDirs})
			errorCount := 0
			for _, r := range results {
				switch {
				case r.Err != nil:
					errorCount++
					logger.Error("vendor failed", "node", r.Node, "err", r.Err)
				case output != "json" && r.Skipped:
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: already vendored (use --force to re-fetch)\n", r.Node)
				case output != "json":
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: vendored\n", r.Node)
				}
			}
			if output == "json" {
				var payload any = vendorResultsToDTO(results)
				if err != nil {
					diagnostics := errorDiagnostics(err, engine.Diagnostic{Code: "vendor_failed", Category: "artifact", Phase: "vendor", Subject: "vendor"})
					if len(results) == 0 {
						payload = errorResultDTO{SchemaVersion: 1, Diagnostics: diagnostics}
					} else {
						payload = vendorFailureDTO{SchemaVersion: 1, Results: vendorResultsToDTO(results), Diagnostics: diagnostics}
					}
				}
				if werr := writeJSON(cmd, payload); werr != nil {
					return werr
				}
			}
			if err != nil {
				return err
			}
			if errorCount > 0 {
				return fmt.Errorf("%d node(s) failed to vendor", errorCount)
			}
			if len(results) == 0 && output != "json" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "nothing to vendor (no remote node sources)")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "restrict to one qualified leaf node (for example prod.vpc)")
	cmd.Flags().BoolVar(&force, "force", false, "re-fetch even if already vendored")
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	return cmd
}
