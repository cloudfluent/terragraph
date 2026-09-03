// Package blueprint defines the terragraph blueprint data model: the set of nodes (independent Terraform/OpenTofu root modules) and edges (output -> input wiring between them) that make up a graph.
package blueprint

import (
	"fmt"
	"strings"
)

// PortKind distinguishes an output port (source of a value) from an input port (a declared variable that receives a value).
type PortKind string

const (
	PortOutput PortKind = "output"
	PortInput  PortKind = "input"
)

// EntityKind distinguishes which keyword prefix a reference used: a plain node ("node.foo...") or a group instance ("use.foo..."). Both resolve to the same PortRef shape and, once built, the same module.Schema shape: a group instance is indistinguishable from a node once its Export has been synthesized into a schema. The zero value is EntityNode, so existing PortRef literals that never set Entity keep meaning "a plain node" without any change.
type EntityKind string

const (
	EntityNode EntityKind = ""
	EntityUse  EntityKind = "use"
)

// PortRef identifies a reference used by an edge endpoint (or an export mapping). It is either:
//   - a specific port, e.g. node.vpc.output.vpc_id or use.checkout.input.vpc_id (Kind is set), or
//   - a bare reference, e.g. node.vpc or use.checkout (Kind is "", Name is ""), used for an ordering-only edge that carries no value.
type PortRef struct {
	Entity EntityKind
	Node   string
	Kind   PortKind
	Name   string
}

// IsPort reports whether this reference points at a specific port (as opposed to a bare reference used only to order execution).
func (p PortRef) IsPort() bool {
	return p.Kind != ""
}

func (p PortRef) prefix() string {
	if p.Entity == EntityUse {
		return "use"
	}
	return "node"
}

func (p PortRef) String() string {
	if !p.IsPort() {
		return fmt.Sprintf("%s.%s", p.prefix(), p.Node)
	}
	return fmt.Sprintf("%s.%s.%s.%s", p.prefix(), p.Node, p.Kind, p.Name)
}

// Node is one independent Terraform/OpenTofu root module participating in the graph. Source is a path relative to the blueprint file.
//
// BackendConfig is optional and lets the same Source be reused by multiple nodes (e.g. the same module for both "dev" and "prod") without state collisions: its entries are passed to `terraform init -backend-config=k=v`, Terraform's own partial backend configuration mechanism. The module must declare a backend block; a non-empty BackendConfig with no backend block (or only a cloud block) is a validation Error. If the module declares backend "local" and this map has no path, graph.Build fills path with an absolute file under <blueprint>/.terragraph/state/<node>.tfstate. An explicit path wins. Two leaves that share a module directory and resolve to identical maps (including both empty) are a validation Error.
//
// Vars is optional and supplies literal input values directly, keyed by variable name: for a value that's genuinely this node's own data (e.g. "this tenant's CIDR is 10.16.0.0/20"), not something that comes from another node's real output. It's merged into the same <node source>/.terragraph.auto.tfvars.json a data edge's resolved value would populate (see engine.Engine.resolveInputs), type-checked against the target variable's declared type the same way, and it's an error for a variable to be set by more than one source at once (two data edges, or a data edge and Vars together). Unlike BackendConfig (always strings), a value here can be any JSON-compatible shape a Terraform variable can hold (string, number, bool, list, or a nested object), so a module needing many inputs can still be wired with one Vars entry per node instead of one edge per variable.
type Node struct {
	Name          string
	Source        string
	BackendConfig map[string]string
	Vars          map[string]any
	// Runtime is the name of a `runtime` block this node explicitly selects (e.g. "tofu" for `runtime = runtime.tofu`), or "" if unset. An unset Runtime doesn't necessarily mean "the built-in terraform default": it may still inherit a runtime from an enclosing Use.Runtime override, or from the blueprint's own default-marked runtime block; see graph.Node.Runtime for the fully resolved value and engine.Engine.runtimeFor for where CLI/built-in fallback is applied on top of that.
	Runtime string
	// Env is optional and sets extra environment variables the node's terraform/tofu subprocess runs with (e.g. AWS_PROFILE, AWS_REGION for a per-account/per-region provider configuration), keyed by variable name. Unlike Runtime (a single, replace-on-override choice), Env cascades by merging: a node's own Env entries win key-by-key over whatever an enclosing Use.Env contributed, rather than discarding the rest of it. See graph.Node.Env for the fully merged result an enclosing chain of Use.Env overrides plus this node's own Env produces.
	Env map[string]string
	// Approve declares how much of this node's plan may be applied without being asked about, or "" if unset. Set it on the specific node where a destructive plan is routine (a module whose resources are recreated by design), rather than reaching for the CLI flag, which grants the same thing to every node in the graph at once. Like Runtime, an unset value may still inherit from an enclosing Use.Approve or the blueprint's own default; see graph.Node.Approve.
	Approve Approve
}

// IsRemote reports whether a Node's Source should be vendored rather than resolved as a local path relative to the blueprint. Mirrors Terraform's own module.source rule: anything not starting with "./" or "../" is remote.
func IsRemote(src string) bool {
	return !strings.HasPrefix(src, "./") && !strings.HasPrefix(src, "../")
}

// Edge connects two nodes. If both endpoints reference specific ports (From.IsPort() && To.IsPort()), this is an explicit data edge: the engine passes From's output value into To's input variable at runtime. If neither endpoint references a port (bare node references), this is an implicit edge: it only constrains execution order between the two nodes and carries no value. Mixing (one endpoint a port, the other bare) is rejected at parse time.
//
// One Edge is not necessarily one `edge` block: a block wiring several outputs of one node into several inputs of another can spell that as nested `input` blocks, which parseEdgeInputs expands into one Edge each before any of this package's callers see them. There is deliberately no field here recording that, so no consumer of a Blueprint can behave differently depending on how a pair of ports was written.
type Edge struct {
	From PortRef
	To   PortRef
}

// IsDataEdge reports whether this edge carries a value (explicit) as opposed to only constraining execution order (implicit).
func (e Edge) IsDataEdge() bool {
	return e.From.IsPort() && e.To.IsPort()
}

// Use instantiates a Group under a new namespace: everything inside the referenced group becomes reachable from this scope only through the group's Export (see Export); the group's own internal nodes are never directly addressable from outside it. As becomes the namespace prefix for the group's expanded nodes (e.g. "checkout" -> "checkout.cluster").
type Use struct {
	GroupName string // which group (by its declared name) to instantiate
	As        string // instance name; the namespace prefix for this instantiation
	Source    string // directory containing the group definition
	// Runtime, if set, names a `runtime` block (resolved in the scope that wrote this Use, not the group's own) that becomes the default for every node this instantiation expands to, unless that node sets its own explicit Runtime. A group definition deliberately has no equivalent of its own: which toolchain deploys a reusable group is a fact about where it's instantiated, not about the group itself, so only the instantiation site (here) can set it, never the group body.
	Runtime string
	// Env, if set, contributes extra environment variables to every node this instantiation expands to (e.g. which AWS account/region/role the whole group deploys into), merged under whatever ambient Env an enclosing Use.Env already contributed and merged under, key-by-key, by each internal node's own Env in turn. Like Runtime, a group definition has no equivalent of its own: which account a reusable group deploys into is a fact about where it's instantiated.
	Env map[string]string
	// Vars is optional and supplies literal input values for this instance, keyed by the group's export input names (not internal node.input paths). Same literal object as Node.Vars: JSON-compatible values, no references to other nodes' outputs, no functions. Graph expansion rewrites each key through the resolved export onto the leaf nodes' Vars maps (see graph.applyUseVars); a group definition has no equivalent, because instance data belongs at the use site.
	Vars map[string]any
	// Approve, if set, becomes the approve level for every node this instantiation expands to, unless that node sets its own. Like Runtime, only the instantiation site can set this and a group definition has no equivalent: how much of a reusable group may be changed unattended is a fact about where it is deployed, not about the group.
	Approve Approve
	// BackendConfig, if set, is merged into every node this instantiation expands to, the same way Env merges: leaf keys win. Instantiation-site fact (bucket, profile, region). Do not invent a remote key from the instance name.
	BackendConfig map[string]string
}

// ExportInput is one input port a group exposes to the outside. To may list more than one internal target: a single exposed value sometimes needs to fan out to several internal nodes that each independently need it, and, unlike execution ordering (which is inferable from the internal graph's shape), there is no way to infer that fan-out from structure alone, so the group author must declare it explicitly.
type ExportInput struct {
	Name string
	To   []PortRef
}

// ExportOutput is one output port a group exposes to the outside. Always a 1:1 passthrough of a single internal output: re-exposing an existing value under an external name never needs fan-in.
type ExportOutput struct {
	Name string
	From PortRef
}

// Export is a group's public interface: the only ports reachable from outside a group instance. Mirrors what a Terraform root module's variables.tf/outputs.tf do for a node, except declared directly in HCL instead of parsed from .tf files. See module.Schema, which an Export gets synthesized into once validated against the group's real internal schemas.
type Export struct {
	Inputs  []ExportInput
	Outputs []ExportOutput
}

// Group is a reusable sub-blueprint template: a named, self-contained bundle of nodes, edges, and nested group instantiations, with an explicit, encapsulated interface (Export). Instantiated via Use.
type Group struct {
	Name   string
	Nodes  []Node
	Edges  []Edge
	Uses   []Use
	Export Export
}

// Default paths used when a blueprint declares no vendor block, or omits one of its fields.
const (
	DefaultVendorDirectory    = "vendor"
	DefaultVendorManifestFile = "vendor.yaml"
)

// VendorConfig customizes where vendored third-party module sources are kept. Everything here is project-wide layout only: per-source behavior (which files to prune from one particular vendored module) lives in vendor.yaml instead, since different upstream repos need different exclusions and a single project-wide list would be the wrong shape.
type VendorConfig struct {
	Directory    string
	ManifestFile string
}

// TFVarsLocation selects where the engine writes the ephemeral, per-node variable file it resolves from data edges and vars before every plan/apply/destroy (see exec.WriteTFVars). Neither location relies on Terraform's *.auto.tfvars.json auto-loading; the engine always passes the file explicitly via -var-file, since auto-loading a name that's ever shared by two nodes (e.g. two instances of the same module source) has no way to keep their values apart.
type TFVarsLocation string

const (
	// TFVarsLocationWorkdir writes to <blueprint dir>/.terragraph/vars/<node>.tfvars.json, next to the node's other engine-managed state (tfdata, plans). Never touches the module's own directory, so nothing needs adding to a module's .gitignore, and a node reused by several instances (backend_config) never collides on a shared filename. This is the default: it keeps every module directory clean, which matters most for a module that's vendored (read-only, not yours to add a .gitignore entry to) or reused across many near-identical instances.
	TFVarsLocationWorkdir TFVarsLocation = "workdir"
	// TFVarsLocationModule writes to <node source>/.terragraph.<node>.tfvars.json, alongside the module's own .tf files, for teams that want a node's resolved input values visible next to its source while debugging. Requires the module's .gitignore to exclude the engine-managed pattern (see docs/execution-model.md); terragraph validate warns about a stale file left behind by a since-renamed or since-removed node sharing that directory, but never deletes one on your behalf.
	TFVarsLocationModule TFVarsLocation = "module"
)

// Runtime is a named, reusable selection of which terraform/tofu binary a node runs against and (optionally) which version of it is expected. Declared once at the top level of a blueprint (or, for a group's own internal nodes, once in that group's own source directory: see Node.Runtime) and referenced by name from as many nodes/uses as need it, rather than repeating a binary path on every node.
type Runtime struct {
	Name string
	// Binary is the command terragraph shells out to for this runtime: a bare name resolved on PATH ("tofu"), or an absolute path to pin an exact install ("/opt/terraform_1.5.7"). Always required: a runtime block with nothing else set would have no identity to select by.
	Binary string
	// Version is an optional, free-form version constraint (e.g. ">= 1.8.0") describing which versions of Binary this runtime block is meant to represent. It is never resolved against the binary's actual reported version (no subprocess is run to check it); it is documentation-only.
	Version string
	// Default marks this runtime as the blueprint-wide fallback for a node that resolves to no runtime at all (no explicit Node.Runtime, no cascaded Use.Runtime). At most one Runtime in a given parse scope may set this; parsing rejects a second. Only a Default set at the blueprint's own top level is ever consulted this way (see engine.Engine.runtimeFor); one set inside a group's own source directory is validated the same way but never applied automatically, so instantiating that group elsewhere can never silently change its nodes' toolchain out from under the caller.
	Default bool
}

// TFVarsConfig customizes where the engine writes per-node ephemeral variable files. Project-wide only, like VendorConfig: a single blueprint uses one location for every node, so a node's resolved inputs are always found the same way regardless of which node you're looking at.
type TFVarsConfig struct {
	Location TFVarsLocation
}

// LockS3 is the nested s3 backend inside a top-level lock block: a conditional-write lock object (not S3 Object Lock / WORM) whose key identifies this graph across machines, never a worktree path.
type LockS3 struct {
	Bucket string
	Key    string
	Region string
}

// LockConfig is the optional top-level lock block. Presence alone enables remote graph-run locking; there is no CLI flag for it. Exactly one nested backend type is set (s3 today; dynamodb/gcs later).
type LockConfig struct {
	S3 *LockS3
}

// Blueprint is the fully parsed graph topology: nodes and the edges between them, plus any group definitions and instantiations. It carries no resource configuration, only wiring.
type Blueprint struct {
	Nodes  []Node
	Edges  []Edge
	Groups []Group
	Uses   []Use
	// Vendor is nil when the blueprint declares no `vendor` block. Use the VendorDirectory/VendorManifestFile accessors, never this field directly, so callers never need to branch on nil.
	Vendor *VendorConfig
	// TFVars is nil when the blueprint declares no `tfvars` block. Use the TFVarsLocation accessor, never this field directly, so callers never need to branch on nil.
	TFVars *TFVarsConfig
	// Lock is nil when the blueprint declares no `lock` block. When set, plan/apply/destroy take a graph-level remote lease after the local flock; every node backend must be remote (see engine lock-backend validation).
	Lock *LockConfig
	// Runtimes holds every `runtime` block declared in this parse scope (see Runtime). Parsing guarantees names are unique and at most one sets Default within this same slice; it does not guarantee every Node.Runtime/Use.Runtime reference in this scope resolves to a name here until validateRuntimes has run, which ParseFile/ParseDir always do before returning.
	Runtimes []Runtime
}

// RuntimeByName returns the runtime declared with this name in this parse scope, or false if none matches.
func (b *Blueprint) RuntimeByName(name string) (Runtime, bool) {
	for _, rt := range b.Runtimes {
		if rt.Name == name {
			return rt, true
		}
	}
	return Runtime{}, false
}

// DefaultRuntime returns this scope's Default-marked runtime, or false if none is marked (see Runtime.Default).
func (b *Blueprint) DefaultRuntime() (Runtime, bool) {
	for _, rt := range b.Runtimes {
		if rt.Default {
			return rt, true
		}
	}
	return Runtime{}, false
}

// VendorDirectory returns the effective vendor directory: the blueprint's configured value, or DefaultVendorDirectory.
func (b *Blueprint) VendorDirectory() string {
	if b.Vendor != nil && b.Vendor.Directory != "" {
		return b.Vendor.Directory
	}
	return DefaultVendorDirectory
}

// VendorManifestFile returns the effective vendor manifest file name: the blueprint's configured value, or DefaultVendorManifestFile.
func (b *Blueprint) VendorManifestFile() string {
	if b.Vendor != nil && b.Vendor.ManifestFile != "" {
		return b.Vendor.ManifestFile
	}
	return DefaultVendorManifestFile
}

// TFVarsLocation returns the effective tfvars location: the blueprint's configured value, or TFVarsLocationWorkdir.
func (b *Blueprint) TFVarsLocation() TFVarsLocation {
	if b.TFVars != nil && b.TFVars.Location != "" {
		return b.TFVars.Location
	}
	return TFVarsLocationWorkdir
}

// NodeByName returns the node with the given name, or false if absent.
func (b *Blueprint) NodeByName(name string) (Node, bool) {
	for _, n := range b.Nodes {
		if n.Name == name {
			return n, true
		}
	}
	return Node{}, false
}
