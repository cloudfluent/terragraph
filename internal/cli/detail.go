package cli

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
)

// detailLimitations are the three fixed caveat strings from #94 §4, verbatim: every detailed result
// states what static inspection cannot claim, in the same words every time, so consumers can match on them.
var detailLimitations = []string{
	"static configuration only; external variable values and live state were not inspected",
	"graph reachability identifies review candidates, not predicted resource changes",
	"reported approval policy is configuration, not execution authorization",
}

// detailResult is the JSON envelope for `terragraph graph --detail` (#94 §4). schema_version versions
// this envelope only; the plain `graph --output json` levels payload keeps its own contract. Every
// array field is non-nil (an empty array is a definite answer) while levels and the nullable location
// pointers use null for "not establishable" — never an empty stand-in.
type detailResult struct {
	SchemaVersion int                   `json:"schema_version"`
	Valid         bool                  `json:"valid"`
	Selection     detailSelectionDTO    `json:"selection"`
	Levels        [][]string            `json:"levels"`
	Nodes         []detailNodeDTO       `json:"nodes"`
	Edges         []detailEdgeDTO       `json:"edges"`
	Diagnostics   []detailDiagnosticDTO `json:"diagnostics"`
	Limitations   []string              `json:"limitations"`
}

// detailSelectionDTO reports what --node asked for: null means whole-graph inspection; a non-null
// value is the requested leaf exactly as spelled, kept even when the name turns out invalid so the
// selection diagnostic and the envelope agree on what was rejected.
type detailSelectionDTO struct {
	Node *string `json:"node"`
}

// detailNodeDTO is one expanded leaf with everything #94 §3 asks a caller to see: identity and
// declaration, module source, instantiation path, port provenance, effective settings with origins,
// and full-graph relationships. It never carries a vars, default, env, or backend_config value.
type detailNodeDTO struct {
	Name        string                  `json:"name"`
	Declaration *sourceLocationDTO      `json:"declaration"`
	Source      detailSourceDTO         `json:"source"`
	GroupPath   []detailInstanceStepDTO `json:"group_path"`
	Inputs      []detailInputDTO        `json:"inputs"`
	Outputs     []detailOutputDTO       `json:"outputs"`
	Runtime     detailRuntimeDTO        `json:"runtime"`
	Approve     detailApproveDTO        `json:"approve"`
	Relations   detailRelationsDTO      `json:"relations"`
}

// detailSourceDTO shows the declared source string sanitized (declared is never the lossless
// original once credentials were stripped; the flag says so) and the resolved directory relative to
// the blueprint directory when possible, keeping ".." segments and falling back to the native
// absolute path only when filepath.Rel cannot express it (e.g. across volumes).
type detailSourceDTO struct {
	Declared  string `json:"declared"`
	Resolved  string `json:"resolved"`
	Sanitized bool   `json:"sanitized_remote,omitempty"`
}

// detailInstanceStepDTO is one group-instantiation level of a node's path, outermost first: the
// instance namespace, the group's declared name, and where the use block and its group block live —
// usually two different files, which is exactly what editing needs to know.
type detailInstanceStepDTO struct {
	Instance  string             `json:"instance"`
	Group     string             `json:"group"`
	Use       *sourceLocationDTO `json:"use"`
	GroupDecl *sourceLocationDTO `json:"group_decl"`
}

// detailInputDTO is one declared module variable with its supply classification. declared_type is
// null when the module states no type constraint (distinct from a failed module read, which Build
// already refused); has_default reports presence only, never the value.
type detailInputDTO struct {
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	DeclaredType *string              `json:"declared_type"`
	Required     bool                 `json:"required"`
	Sensitive    bool                 `json:"sensitive"`
	HasDefault   bool                 `json:"has_default"`
	Location     *sourceLocationDTO   `json:"location"`
	Source       detailInputSourceDTO `json:"source"`
}

// detailInputSourceDTO explains how an input is supplied (#94 §3.2). Kinds are graph.InputSource's
// vocabulary verbatim (edge|node_vars|use_vars|plugin_input|external_or_default|external_required|
// conflict); the external kinds carry no subject or location because no declaration exists to name,
// and conflict nests each individual supply as a candidate so the fix is visible at the slot.
type detailInputSourceDTO struct {
	Kind        string                 `json:"kind"`
	Subject     string                 `json:"subject"`
	Location    *sourceLocationDTO     `json:"location"`
	MappingPath []detailMappingStepDTO `json:"mapping_path"`
	Candidates  []detailInputSourceDTO `json:"candidates,omitempty"`
}

// detailMappingStepDTO is one export hop between the original declaration and the final leaf, so a
// fanned-out supply stays explainable at each destination (#94 §3.2).
type detailMappingStepDTO struct {
	Via      string             `json:"via"`
	Location *sourceLocationDTO `json:"location"`
}

// detailOutputDTO is one declared module output; declared_type is null for an ordinary Terraform
// root output with no static type, not a claim about the observed value.
type detailOutputDTO struct {
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	DeclaredType *string            `json:"declared_type"`
	Sensitive    bool               `json:"sensitive"`
	Location     *sourceLocationDTO `json:"location"`
}

// detailRuntimeDTO is the effective runtime plus its selection origin (node|use|root|cli|builtin).
// Location pins the selector (the runtime = attribute or the default-marked block, which selects
// itself) while DefinitionLocation pins the runtime block that defined the binary; the two differ
// for a node referencing a named runtime. Version is the declared constraint only — nothing ever
// verified the installed binary against it.
type detailRuntimeDTO struct {
	Binary             string             `json:"binary"`
	Name               *string            `json:"name"`
	Version            *string            `json:"version"`
	Origin             string             `json:"origin"`
	Location           *sourceLocationDTO `json:"location"`
	DefinitionLocation *sourceLocationDTO `json:"definition_location"`
	Use                *detailUseRefDTO   `json:"use"`
}

// detailApproveDTO is the effective approve level plus its origin (node|use|cli|builtin). A
// declaration always beats the CLI layer, so --approve all never widens a node that declared safe;
// the reported policy explains configuration and authorizes nothing (see detailLimitations).
type detailApproveDTO struct {
	Effective string             `json:"effective"`
	Origin    string             `json:"origin"`
	Location  *sourceLocationDTO `json:"location"`
	Use       *detailUseRefDTO   `json:"use"`
}

// detailUseRefDTO locates the enclosing use block when a setting or runtime came from one.
type detailUseRefDTO struct {
	Name     string             `json:"name"`
	Location *sourceLocationDTO `json:"location"`
}

// detailRelationsDTO holds the four full-graph relationship lists; they are computed over the whole
// blueprint even when --node narrows the output, because review scope is a fact about the graph,
// not about the slice printed. Empty arrays are definite answers (nothing depends on this node).
type detailRelationsDTO struct {
	DependsOn   []string `json:"depends_on"`
	Dependents  []string `json:"dependents"`
	Ancestors   []string `json:"ancestors"`
	Descendants []string `json:"descendants"`
}

// detailEdgeDTO is one normalized connection: data edges name ports, ordering edges carry null
// ports, and distinct port connections between one pair stay separate rows. mapping_path is the
// export hops the declaration traversed, empty for a plain node-to-node edge.
type detailEdgeDTO struct {
	Kind        string                 `json:"kind"`
	From        detailEndpointDTO      `json:"from"`
	To          detailEndpointDTO      `json:"to"`
	Declaration *sourceLocationDTO     `json:"declaration"`
	MappingPath []detailMappingStepDTO `json:"mapping_path"`
}

// detailEndpointDTO is one edge endpoint; Port is null exactly when the edge is ordering-only.
type detailEndpointDTO struct {
	Node string  `json:"node"`
	Port *string `json:"port"`
}

// detailDiagnosticDTO is the envelope's diagnostic shape (#94 §4): every field is present, source is
// the nullable declaration location when one exists. Codes reuse graph.Problem.Code (phase
// "validate") and the CLI's established argument vocabulary (phase "arguments") or "select" for
// --node resolution — no new coding scheme.
type detailDiagnosticDTO struct {
	Code     string             `json:"code"`
	Severity string             `json:"severity"`
	Phase    string             `json:"phase"`
	Subject  string             `json:"subject"`
	Message  string             `json:"message"`
	Remedy   string             `json:"remedy"`
	Source   *sourceLocationDTO `json:"source"`
}

// graphDetailOptions carries the graph command's flag state into the detail path. Flag-change
// booleans (not values) distinguish an explicit --tofu/--approve from the indistinguishable
// built-in defaults — only the command, which saw the flags, can tell those origins apart.
type graphDetailOptions struct {
	blueprintPath     *string
	binaryOf          func() exec.Binary
	loggerOf          func() *slog.Logger
	nodes             []string
	nodeSelected      bool
	downstreamChanged bool
	approveValue      string
	approveChanged    bool
	tofuChanged       bool
	format            string
	output            string
}

// runGraphDetail is the single resolution path for `graph --detail` (#94): both renderers consume
// the same detailResult. Inspection never takes the process lock, never runs a runtime, and never
// writes anything under .terragraph — it is loadEngine (graph's existing loader) plus static reads.
func runGraphDetail(cmd *cobra.Command, o graphDetailOptions) error {
	if o.format == "dot" {
		return detailArgumentError(cmd, o, "--format dot cannot render detailed inspection; use the default list rendering for text or --output json for structured inspection")
	}
	if o.downstreamChanged {
		return detailArgumentError(cmd, o, "--downstream selects an execution scope; graph --detail inspects declarations: pass one leaf with --node <name>, or drop --downstream for whole-graph inspection")
	}
	if len(o.nodes) > 1 {
		return detailArgumentError(cmd, o, fmt.Sprintf("graph --detail inspects exactly one leaf; got %d selections (%s); pass --node once with one expanded leaf name", len(o.nodes), joinNames(o.nodes)))
	}
	if len(o.nodes) == 1 && o.nodes[0] == "" {
		return detailArgumentError(cmd, o, "--node requires a leaf name; pass one expanded leaf name from graph output")
	}
	policy, err := blueprint.ParseApprove(o.approveValue)
	if err != nil {
		return detailArgumentError(cmd, o, err.Error())
	}

	e, err := loadEngine(cmd, o.blueprintPath, o.binaryOf, o.loggerOf)
	if err != nil {
		return detailFailure(cmd, o, detailDiagnosticDTO{
			Code: "load_failed", Severity: "error", Phase: "load", Subject: "graph",
			Message: err.Error(), Remedy: "fix the reported blueprint, source, or vendoring problem and rerun", Source: errorLocation(err),
		}, err)
	}

	problems := e.Validate()
	for _, p := range problems {
		label := "ERROR"
		if !p.IsError() {
			label = "WARNING"
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[%s] %s\n", label, p.Message)
	}
	diagnostics := detailProblemDiagnostics(problems)
	valid := !hasErrors(problems)

	res := &detailResult{SchemaVersion: 1, Valid: valid, Selection: detailSelectionDTO{}, Nodes: []detailNodeDTO{}, Edges: []detailEdgeDTO{}, Diagnostics: diagnostics, Limitations: detailLimitations}
	if o.nodeSelected {
		want := o.nodes[0]
		res.Selection.Node = &want
		if _, ok := e.Graph.Nodes[want]; !ok {
			res.Diagnostics = append(res.Diagnostics, detailSelectionDiagnostic(e.Graph, want))
			res.Valid = false
		}
	}

	levels, levelErr := graph.Levels(e.Graph)
	if levelErr != nil {
		// A cycle leaves ordering unavailable but the graph itself constructed: levels stay null
		// (never an empty plan) while nodes, edges, and relationships remain, per #94 §5.
		res.Levels = nil
	} else {
		res.Levels = levels
		if res.Levels == nil {
			res.Levels = [][]string{}
		}
	}

	// #94 §5: validation errors and cycles keep the constructed nodes, connections, and
	// reachability (valid=false), because Build succeeded and only ordering or wiring checks
	// failed; only a failed load or an unresolvable --node leaves the result empty.
	names := sortedNodeNames(e.Graph)
	if res.Selection.Node != nil {
		names = []string{*res.Selection.Node}
	}
	if len(names) > 0 {
		res.Nodes = buildDetailNodes(e, names, o, policy)
		res.Edges = buildDetailEdges(e.Graph, res.Selection.Node)
	}

	if o.output == "json" {
		if err := writeJSON(cmd, res); err != nil {
			return err
		}
	} else {
		renderDetailText(cmd.OutOrStdout(), res)
	}
	if !res.Valid {
		for _, d := range res.Diagnostics {
			if d.Code == "unknown_node" {
				return fmt.Errorf("graph --detail: %s; %s", d.Message, d.Remedy)
			}
		}
		return fmt.Errorf("blueprint has %d error(s); run \"terragraph validate\" for details", countErrors(problems))
	}
	return nil
}

// detailArgumentError reports an option-combination failure. #94 §5 wants the same envelope with a
// remedy when output is possible (JSON mode, before anything is loaded); text mode keeps the
// existing Cobra error contract.
func detailArgumentError(cmd *cobra.Command, o graphDetailOptions, message string) error {
	err := fmt.Errorf("%s", message)
	if o.output != "json" {
		return err
	}
	res := &detailResult{
		SchemaVersion: 1, Selection: detailSelectionDTO{}, Levels: nil,
		Nodes: []detailNodeDTO{}, Edges: []detailEdgeDTO{}, Limitations: detailLimitations,
		Diagnostics: []detailDiagnosticDTO{{
			Code: "invalid_arguments", Severity: "error", Phase: "arguments", Subject: "graph",
			Message: message, Remedy: "check flag combinations against terragraph graph --help",
		}},
	}
	if werr := writeJSON(cmd, res); werr != nil {
		return werr
	}
	return err
}

// detailFailure emits the load/build-failure envelope (#94 §5: valid=false, levels=null, empty
// nodes/edges, cause with location and remedy where available) and still returns the original
// error so the exit code is nonzero. No partial reparse is attempted.
func detailFailure(cmd *cobra.Command, o graphDetailOptions, d detailDiagnosticDTO, cause error) error {
	if o.output != "json" {
		return cause
	}
	res := &detailResult{
		SchemaVersion: 1, Selection: detailSelectionDTO{}, Levels: nil,
		Nodes: []detailNodeDTO{}, Edges: []detailEdgeDTO{}, Limitations: detailLimitations,
		Diagnostics: []detailDiagnosticDTO{d},
	}
	if werr := writeJSON(cmd, res); werr != nil {
		return werr
	}
	return cause
}

// detailProblemDiagnostics maps graph problems onto the envelope's diagnostic shape mechanically:
// code/subject/message/remedy reused verbatim, phase "validate", severity from IsError. Source stays
// null because structural problems carry no declaration location today.
func detailProblemDiagnostics(problems []graph.Problem) []detailDiagnosticDTO {
	out := make([]detailDiagnosticDTO, 0, len(problems))
	for _, p := range problems {
		severity := "warning"
		if p.IsError() {
			severity = "error"
		}
		out = append(out, detailDiagnosticDTO{Code: p.Code, Severity: severity, Phase: "validate", Subject: p.Subject, Message: p.Message, Remedy: p.Remedy})
	}
	sortDiagnostics(out)
	return out
}

// sortDiagnostics fixes a total order (code, subject, message) because e.Validate iterates node
// maps, so insertion order is map-churn; the envelope promises stable ordering for the same input.
func sortDiagnostics(d []detailDiagnosticDTO) {
	sort.SliceStable(d, func(i, j int) bool {
		if d[i].Code != d[j].Code {
			return d[i].Code < d[j].Code
		}
		if d[i].Subject != d[j].Subject {
			return d[i].Subject < d[j].Subject
		}
		return d[i].Message < d[j].Message
	})
}

// detailSelectionDiagnostic explains a --node value that names no expanded leaf: a group-instance
// prefix or a typo gets the matching leaf candidates and usage guidance instead of silence (#94 §2).
func detailSelectionDiagnostic(g *graph.Graph, want string) detailDiagnosticDTO {
	candidates := leafCandidates(g, want)
	message := fmt.Sprintf("%q does not name an expanded leaf", want)
	remedy := "run \"terragraph graph\" to list leaf names and pass one with --node"
	if len(candidates) > 0 {
		if want == firstSegment(want) {
			message = fmt.Sprintf("%q is a group instance or prefix, not a leaf", want)
		}
		remedy = fmt.Sprintf("inspect one leaf by its full expanded name; matching leaves: %s", joinNames(candidates))
	}
	return detailDiagnosticDTO{Code: "unknown_node", Severity: "error", Phase: "select", Subject: "node." + want, Message: message, Remedy: remedy}
}

// leafCandidates lists leaves worth offering for a rejected --node value: exact matches, children of
// the requested prefix, parents of it, and leaves sharing the first dotted segment (which catches
// typos in the leaf half like "checkout.clustr"). Sorted for stable messages.
func leafCandidates(g *graph.Graph, want string) []string {
	seg := firstSegment(want)
	out := []string{}
	for name := range g.Nodes {
		if name == want || strings.HasPrefix(name, want+".") || strings.HasPrefix(want, name+".") || strings.HasPrefix(name, seg+".") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func firstSegment(name string) string {
	if i := strings.Index(name, "."); i >= 0 {
		return name[:i]
	}
	return name
}

func hasErrors(problems []graph.Problem) bool { return countErrors(problems) > 0 }

func countErrors(problems []graph.Problem) int {
	n := 0
	for _, p := range problems {
		if p.IsError() {
			n++
		}
	}
	return n
}

func sortedNodeNames(g *graph.Graph) []string {
	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// buildDetailNodes assembles the node DTOs for the given scope (whole graph or the one selection).
func buildDetailNodes(e *engine.Engine, names []string, o graphDetailOptions, policy blueprint.Approve) []detailNodeDTO {
	out := make([]detailNodeDTO, 0, len(names))
	for _, name := range names {
		n := e.Graph.Nodes[name]
		if n == nil {
			continue
		}
		declared, sanitized := sanitizeSource(n.Source)
		dto := detailNodeDTO{
			Name:        name,
			Declaration: locToDTO(n.Prov.Decl),
			Source:      detailSourceDTO{Declared: declared, Resolved: relPath(e.BaseDir, n.Dir), Sanitized: sanitized},
			GroupPath:   buildGroupPath(n.Prov.Path),
			Inputs:      buildDetailInputs(e.Graph, n),
			Outputs:     buildDetailOutputs(n),
			Relations:   buildDetailRelations(e.Graph, name),
		}
		dto.Runtime = buildDetailRuntime(e.ResolveRuntime(name, o.tofuChanged))
		dto.Approve = buildDetailApprove(e.ResolveApprove(name, o.approveChanged, policy))
		out = append(out, dto)
	}
	return out
}

func buildGroupPath(path []graph.InstanceStep) []detailInstanceStepDTO {
	out := make([]detailInstanceStepDTO, 0, len(path))
	for _, s := range path {
		out = append(out, detailInstanceStepDTO{Instance: s.Instance, Group: s.Group, Use: locToDTO(s.Use), GroupDecl: locToDTO(s.GroupDecl)})
	}
	return out
}

// buildDetailInputs classifies every variable the node's module declares (#94 §3.2) — never the
// other way around, because a supply targeting an undeclared variable is Validate's missing_input
// problem, not a port. Sorted by name; port locations join the module's bare filename with the
// node's resolved Dir, which is where the declaration actually lives.
func buildDetailInputs(g *graph.Graph, n *graph.Node) []detailInputDTO {
	sources := g.InputSourcesFor(n.Name)
	out := make([]detailInputDTO, 0, len(sources))
	for varName, src := range sources {
		v := n.Schema.Variables[varName]
		dto := detailInputDTO{
			Name: varName, Description: v.Description, Required: v.Required, Sensitive: v.Sensitive, HasDefault: v.HasDefault,
			Location: portLocToDTO(n.Dir, v.Loc.File, v.Loc.Line, v.Loc.Column),
			Source:   buildInputSource(src),
		}
		if v.Type != "" {
			t := v.Type
			dto.DeclaredType = &t
		}
		out = append(out, dto)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// buildInputSource maps one graph.InputSource onto its DTO, including conflict candidates (one
// level deep: candidates are individual supplies and never nest further).
func buildInputSource(src graph.InputSource) detailInputSourceDTO {
	dto := detailInputSourceDTO{Kind: src.Kind, Subject: src.Subject, Location: locToDTO(src.Decl), MappingPath: buildMappingPath(src.MappingPath)}
	if len(src.Candidates) > 0 {
		dto.Candidates = make([]detailInputSourceDTO, 0, len(src.Candidates))
		for _, c := range src.Candidates {
			dto.Candidates = append(dto.Candidates, buildInputSource(c))
		}
	}
	return dto
}

func buildMappingPath(steps []graph.MappingStep) []detailMappingStepDTO {
	out := make([]detailMappingStepDTO, 0, len(steps))
	for _, s := range steps {
		out = append(out, detailMappingStepDTO{Via: s.Via, Location: locToDTO(s.Decl)})
	}
	return out
}

// buildDetailOutputs lists the module's declared outputs with type/sensitivity where the selected
// runtime's declarations state them; a Terraform root output with no static type reports null.
func buildDetailOutputs(n *graph.Node) []detailOutputDTO {
	if n.Schema == nil {
		return []detailOutputDTO{}
	}
	names := make([]string, 0, len(n.Schema.Outputs))
	for name := range n.Schema.Outputs {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]detailOutputDTO, 0, len(names))
	for _, name := range names {
		dto := detailOutputDTO{Name: name}
		if o, ok := n.Schema.OutputDetails[name]; ok {
			dto.Description = o.Description
			dto.Sensitive = o.Sensitive
			dto.Location = portLocToDTO(n.Dir, o.Loc.File, o.Loc.Line, o.Loc.Column)
			if o.Type != "" {
				t := o.Type
				dto.DeclaredType = &t
			}
		}
		out = append(out, dto)
	}
	return out
}

func buildDetailRuntime(r engine.RuntimeResolution) detailRuntimeDTO {
	dto := detailRuntimeDTO{Binary: string(r.Binary), Origin: string(r.Origin), Location: locToDTO(r.Decl), DefinitionLocation: locToDTO(r.Def)}
	if r.RuntimeName != "" {
		name := r.RuntimeName
		dto.Name = &name
	}
	if r.Version != "" {
		v := r.Version
		dto.Version = &v
	}
	if r.UseName != "" {
		dto.Use = &detailUseRefDTO{Name: r.UseName, Location: locToDTO(r.UseDecl)}
	}
	return dto
}

func buildDetailApprove(a engine.ApproveResolution) detailApproveDTO {
	dto := detailApproveDTO{Effective: string(a.Policy), Origin: string(a.Origin), Location: locToDTO(a.Decl)}
	if a.UseName != "" {
		dto.Use = &detailUseRefDTO{Name: a.UseName, Location: locToDTO(a.UseDecl)}
	}
	return dto
}

func buildDetailRelations(g *graph.Graph, name string) detailRelationsDTO {
	rel := g.RelationshipsFor(name)
	return detailRelationsDTO{DependsOn: rel.DependsOn, Dependents: rel.Dependents, Ancestors: rel.Ancestors, Descendants: rel.Descendants}
}

// buildDetailEdges returns the whole graph's connections, or only those directly incident to the
// selected leaf when --node narrows; relationship lists always come from the full graph regardless.
func buildDetailEdges(g *graph.Graph, selected *string) []detailEdgeDTO {
	out := []detailEdgeDTO{}
	for _, e := range g.Edges {
		if selected != nil && e.From.Node != *selected && e.To.Node != *selected {
			continue
		}
		dto := detailEdgeDTO{Declaration: locToDTO(e.Loc), MappingPath: buildMappingPath(e.MappingPath)}
		if e.IsDataEdge() {
			dto.Kind = "data"
			from, to := e.From.Name, e.To.Name
			dto.From, dto.To = detailEndpointDTO{Node: e.From.Node, Port: &from}, detailEndpointDTO{Node: e.To.Node, Port: &to}
		} else {
			dto.Kind = "ordering"
			dto.From, dto.To = detailEndpointDTO{Node: e.From.Node}, detailEndpointDTO{Node: e.To.Node}
		}
		out = append(out, dto)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.From.Node != b.From.Node {
			return a.From.Node < b.From.Node
		}
		if portKey(a.From.Port) != portKey(b.From.Port) {
			return portKey(a.From.Port) < portKey(b.From.Port)
		}
		if a.To.Node != b.To.Node {
			return a.To.Node < b.To.Node
		}
		if portKey(a.To.Port) != portKey(b.To.Port) {
			return portKey(a.To.Port) < portKey(b.To.Port)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return locLess(a.Declaration, b.Declaration)
	})
	return out
}

func portKey(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// locLess orders two nullable locations (declaration is the final edge sort key); a null location
// sorts first so the order stays total and deterministic.
func locLess(a, b *sourceLocationDTO) bool {
	if a == nil || b == nil {
		return a == nil && b != nil
	}
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column < b.Column
}

// locToDTO converts a blueprint location, null when File is empty: the zero Loc is how "no file
// declaration" (CLI option or built-in default) is spelled, and it must stay distinct from line 0.
func locToDTO(l blueprint.Loc) *sourceLocationDTO {
	if l.File == "" {
		return nil
	}
	return &sourceLocationDTO{File: l.File, Line: l.Line, Column: l.Column}
}

// portLocToDTO joins a module port's bare filename (module.Loc deliberately carries no machine
// path) with the node's resolved directory, which is where the inspected declaration lives.
func portLocToDTO(dir, file string, line, col int) *sourceLocationDTO {
	if file == "" {
		return nil
	}
	return &sourceLocationDTO{File: filepath.Join(dir, file), Line: line, Column: col}
}

// relPath prefers a path relative to the blueprint directory (#94 §3.1), keeping ".." segments for
// external modules; the native absolute path appears only when Rel cannot express the pair.
func relPath(baseDir, dir string) string {
	if rel, err := filepath.Rel(baseDir, dir); err == nil {
		return rel
	}
	return dir
}

// credentialQueryKeys are query parameter names that authenticate a fetch; any of them in a module
// source is a credential to strip before display, whatever the host does with it.
var credentialQueryKeys = map[string]bool{
	"token": true, "access_token": true, "api_key": true, "apikey": true,
	"password": true, "sshkey": true, "private_token": true, "auth": true,
}

// sanitizeSource strips credentials from URL-shaped module sources (userinfo and credential-bearing
// query parameters), keeping scheme, host, and path so the source stays recognizable but never
// replayable (#94 §5). The second return reports whether anything was removed, flagging declared as
// a redacted remote source rather than a lossless reference. Non-URL sources pass through unchanged.
func sanitizeSource(src string) (string, bool) {
	parsed, prefix := src, ""
	if i := strings.Index(src, "::"); i >= 0 && !strings.Contains(src[:i], "/") {
		prefix, parsed = src[:i+2], src[i+2:]
	}
	u, err := url.Parse(parsed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return src, false
	}
	original := u.String()
	u.User = nil
	q := u.Query()
	for key := range q {
		if credentialQueryKeys[strings.ToLower(key)] {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	if out := u.String(); out != original {
		return prefix + out, true
	}
	return src, false
}

// renderDetailText renders the same detailResult the JSON envelope serializes (#94 §4 example
// shape): per-node blocks with source, group path, per-input supply provenance, ports, effective
// settings with origins, and review scope, each closing on the limitation line.
func renderDetailText(w io.Writer, res *detailResult) {
	for i := range res.Nodes {
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		renderDetailNodeText(w, &res.Nodes[i])
	}
}

func renderDetailNodeText(w io.Writer, n *detailNodeDTO) {
	_, _ = fmt.Fprintf(w, "node %s\n", n.Name)
	_, _ = fmt.Fprintf(w, "  module: %s\n", n.Source.Declared)
	_, _ = fmt.Fprintf(w, "  module dir: %s\n", n.Source.Resolved)
	for _, step := range n.GroupPath {
		_, _ = fmt.Fprintf(w, "  group: %s (%s)\n", step.Instance, step.Group)
	}
	_, _ = fmt.Fprintf(w, "  declaration: %s\n", locString(n.Declaration))

	_, _ = fmt.Fprintf(w, "  inputs:\n")
	for _, in := range n.Inputs {
		parts := []string{typeLabel(in.DeclaredType)}
		if in.Required {
			parts = append(parts, "required")
		} else {
			parts = append(parts, "optional")
		}
		if in.HasDefault {
			parts = append(parts, "has default")
		}
		if in.Sensitive {
			parts = append(parts, "sensitive")
		}
		_, _ = fmt.Fprintf(w, "    %s: %s\n", in.Name, strings.Join(parts, ", "))
		renderInputSourceText(w, &in.Source)
	}

	_, _ = fmt.Fprintf(w, "  outputs:\n")
	for _, out := range n.Outputs {
		parts := []string{typeLabel(out.DeclaredType)}
		if out.Sensitive {
			parts = append(parts, "sensitive")
		}
		_, _ = fmt.Fprintf(w, "    %s: %s\n", out.Name, strings.Join(parts, ", "))
		if out.Location != nil {
			_, _ = fmt.Fprintf(w, "      declared at: %s\n", locString(out.Location))
		}
	}

	_, _ = fmt.Fprintf(w, "  runtime: %s%s\n", n.Runtime.Binary, runtimeQualifier(&n.Runtime))
	_, _ = fmt.Fprintf(w, "  approve: %s%s\n", n.Approve.Effective, approveQualifier(&n.Approve))

	_, _ = fmt.Fprintf(w, "  depends on: %s\n", nameList(n.Relations.DependsOn))
	_, _ = fmt.Fprintf(w, "  dependents: %s\n", nameList(n.Relations.Dependents))
	_, _ = fmt.Fprintf(w, "  ancestors: %s\n", nameList(n.Relations.Ancestors))
	_, _ = fmt.Fprintf(w, "  descendants to review: %s\n", nameList(n.Relations.Descendants))
	_, _ = fmt.Fprintln(w, "  limitation: downstream resource changes require plan evidence")
}

// renderInputSourceText spells one supply out: its declaring subject, every export hop in order,
// and the original declaration site; external kinds state presence facts, never values.
func renderInputSourceText(w io.Writer, src *detailInputSourceDTO) {
	const ind = "      "
	switch src.Kind {
	case "edge":
		_, _ = fmt.Fprintf(w, "%sedge: %s\n", ind, src.Subject)
	case "node_vars", "use_vars", "plugin_input":
		_, _ = fmt.Fprintf(w, "%ssupplied by: %s\n", ind, src.Subject)
	case "external_or_default":
		_, _ = fmt.Fprintf(w, "%sexternal: no blueprint supply; the module declares a default (its value was not inspected)\n", ind)
	case "external_required":
		_, _ = fmt.Fprintf(w, "%sexternal: no blueprint supply and no declared default; external supply must be checked\n", ind)
	case "conflict":
		_, _ = fmt.Fprintf(w, "%sconflict: multiple blueprint supplies target this input\n", ind)
		for i := range src.Candidates {
			renderInputSourceText(w, &src.Candidates[i])
		}
	}
	for _, m := range src.MappingPath {
		_, _ = fmt.Fprintf(w, "%svia: %s\n", ind, m.Via)
	}
	if src.Location != nil {
		_, _ = fmt.Fprintf(w, "%sdeclared at: %s\n", ind, locString(src.Location))
	}
}

// runtimeQualifier names where the effective runtime came from, including the declared-constraint
// caveat: a version was never checked against the installed binary and must not read as one.
func runtimeQualifier(r *detailRuntimeDTO) string {
	qual := ""
	if r.Version != nil {
		qual = fmt.Sprintf(" (version %s is a declared constraint, not a verified install)", *r.Version)
	}
	switch r.Origin {
	case "builtin":
		return " (built-in default)" + qual
	case "cli":
		return " (selected by --tofu)" + qual
	case "root":
		return fmt.Sprintf(" (root default runtime, declared at %s)%s", locString(r.Location), qual)
	case "use":
		return fmt.Sprintf(" (from use %s, declared at %s)%s", r.Use.Name, locString(r.Use.Location), qual)
	case "node":
		return fmt.Sprintf(" (declared at %s)%s", locString(r.Location), qual)
	}
	return qual
}

// approveQualifier names the approve origin; every origin repeats that the reported policy is
// configuration, not authority to run anything.
func approveQualifier(a *detailApproveDTO) string {
	switch a.Origin {
	case "builtin":
		return " (built-in default; not execution authorization)"
	case "cli":
		return " (set by --approve; not execution authorization)"
	case "use":
		return fmt.Sprintf(" (from use %s, declared at %s; not execution authorization)", a.Use.Name, locString(a.Use.Location))
	case "node":
		return fmt.Sprintf(" (declared at %s; not execution authorization)", locString(a.Location))
	}
	return " (not execution authorization)"
}

func typeLabel(t *string) string {
	if t == nil {
		return "(no declared type)"
	}
	return *t
}

func locString(l *sourceLocationDTO) string {
	if l == nil {
		return "(no declaration)"
	}
	return fmt.Sprintf("%s:%d:%d", l.File, l.Line, l.Column)
}

func nameList(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return joinNames(names)
}
