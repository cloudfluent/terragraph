package engine

import (
	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// RuntimeOrigin names which layer of the runtime ladder supplied a node's binary. It is a reporting vocabulary for inspection (#94 §3.4), not a selection rule: the ladder itself is resolveRuntime, the one path execution already walks.
type RuntimeOrigin string

const (
	RuntimeOriginNode    RuntimeOrigin = "node"
	RuntimeOriginUse     RuntimeOrigin = "use"
	RuntimeOriginRoot    RuntimeOrigin = "root"
	RuntimeOriginCLI     RuntimeOrigin = "cli"
	RuntimeOriginBuiltin RuntimeOrigin = "builtin"
)

// RuntimeResolution is one node's effective runtime plus where it came from. Decl is the declaration that selected the runtime (node/use `runtime` attribute, or the Default-marked block for the root layer, which is its own selector); Def is the runtime block definition that supplied binary/name/version. Version is the declared constraint only (see blueprint.Runtime.Version): nothing ever verified the installed binary against it, and reporting it as one would be a claim terragraph never made.
type RuntimeResolution struct {
	Binary      exec.Binary
	Origin      RuntimeOrigin
	RuntimeName string
	Version     string
	Decl        blueprint.Loc
	Def         blueprint.Loc
	// UseName/UseDecl locate the enclosing use block when Origin is "use"; zero otherwise.
	UseName string
	UseDecl blueprint.Loc
}

// ApproveOrigin names the layer that supplied a node's effective approve level; same reporting-only role as RuntimeOrigin.
type ApproveOrigin string

const (
	ApproveOriginNode    ApproveOrigin = "node"
	ApproveOriginUse     ApproveOrigin = "use"
	ApproveOriginCLI     ApproveOrigin = "cli"
	ApproveOriginBuiltin ApproveOrigin = "builtin"
)

// ApproveResolution is one node's effective approve level plus where it came from. Decl is the node/use approve attribute; zero for the CLI and built-in layers, which have no declaration site.
type ApproveResolution struct {
	Policy  blueprint.Approve
	Origin  ApproveOrigin
	Decl    blueprint.Loc
	UseName string
	UseDecl blueprint.Loc
}

// ResolveRuntime reports name's effective binary and its selection origin. cliBinaryExplicit says e.Binary came from an explicit --tofu flag rather than the built-in terraform default: the two are one indistinguishable exec.Binary at execution time, so only the caller (who saw the flag) can tell the residual layer's origins apart. Walks the exact ladder runtimeFor executes against; there is no inspection-specific precedence.
func (e *Engine) ResolveRuntime(name string, cliBinaryExplicit bool) RuntimeResolution {
	return e.resolveRuntime(name, cliBinaryExplicit)
}

// ResolveApprove reports name's effective approve level and its origin. cliExplicit says --approve was passed at all and cliValue is what it said; an explicit --approve safe and the omitted-flag built-in safe are the same policy through different origins, and that distinction is exactly what inspection needs to preserve.
func (e *Engine) ResolveApprove(name string, cliExplicit bool, cliValue blueprint.Approve) ApproveResolution {
	return e.resolveApprove(name, cliExplicit, cliValue)
}

// resolveRuntime is the single runtime precedence ladder both execution (runtimeFor) and inspection (ResolveRuntime) walk: the node's resolved blueprint.Runtime with its SettingSource attribution (node attribute, or the nearest use whose choice survived the cascade); then the root blueprint's Default-marked runtime — never a group's own default, because e.Blueprint is the root scope only; then e.Binary, tagged "cli" or "builtin" by who the caller says selected it. A declaration always beats the CLI layer: --tofu can only fill a gap nothing in the blueprint spoke to.
func (e *Engine) resolveRuntime(name string, cliBinaryExplicit bool) RuntimeResolution {
	if rt := e.Graph.Nodes[name].Runtime; rt != nil {
		src := e.Graph.Nodes[name].RuntimeSource
		return RuntimeResolution{
			Binary:      exec.Binary(rt.Binary),
			Origin:      RuntimeOrigin(src.From),
			RuntimeName: rt.Name,
			Version:     rt.Version,
			Decl:        src.Decl,
			Def:         rt.Loc,
			UseName:     src.UseName,
			UseDecl:     src.UseDecl,
		}
	}
	if e.Blueprint != nil {
		if rt, ok := e.Blueprint.DefaultRuntime(); ok {
			// A Default-marked block is its own selector: `default = true` inside it did the choosing, so Decl and Def both name the runtime block.
			return RuntimeResolution{
				Binary:      exec.Binary(rt.Binary),
				Origin:      RuntimeOriginRoot,
				RuntimeName: rt.Name,
				Version:     rt.Version,
				Decl:        rt.Loc,
				Def:         rt.Loc,
			}
		}
	}
	origin := RuntimeOriginBuiltin
	if cliBinaryExplicit {
		origin = RuntimeOriginCLI
	}
	return RuntimeResolution{Binary: e.Binary, Origin: origin}
}

// resolveApprove is the single approve ladder both execution (approveFor) and inspection (ResolveApprove) walk: the node's resolved Approve with its SettingSource attribution, then the explicit CLI value, then the built-in safe. A declaration always beats the CLI layer — --approve=all can only fill a gap, never widen a node or use that declared safe.
func (e *Engine) resolveApprove(name string, cliExplicit bool, cliValue blueprint.Approve) ApproveResolution {
	if a := e.Graph.Nodes[name].Approve; a != "" {
		src := e.Graph.Nodes[name].ApproveSource
		return ApproveResolution{Policy: a, Origin: ApproveOrigin(src.From), Decl: src.Decl, UseName: src.UseName, UseDecl: src.UseDecl}
	}
	if cliExplicit {
		return ApproveResolution{Policy: cliValue, Origin: ApproveOriginCLI}
	}
	return ApproveResolution{Policy: blueprint.ApproveSafe, Origin: ApproveOriginBuiltin}
}
