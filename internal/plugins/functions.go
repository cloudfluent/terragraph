package plugins

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	sdk "github.com/cloudfluent/terragraph/plugin"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// Evaluation owns the static sessions until group expansion completes; it exposes no variables or runtime resolvers to HCL.
type Evaluation struct {
	Context  *hcl.EvalContext
	sessions []*Session
	workDir  string
}

// Evaluate loads only explicitly locked pure functions and refuses unsupported features instead of silently ignoring policy.
func Evaluate(ctx context.Context, path string) (_ *Evaluation, resultErr error) {
	configs, dir, err := blueprint.LoadPlugins(path)
	if err != nil {
		return nil, err
	}
	settings := settingsFor(ctx)
	ctx = context.WithValue(ctx, loggingKey{}, settings)
	e := &Evaluation{Context: &hcl.EvalContext{Functions: map[string]function.Function{}}}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, e.Close())
		}
	}()
	if len(configs) == 0 {
		return e, nil
	}
	packages := make([]Package, len(configs))
	for i, config := range configs {
		p, err := Resolve(dir, config)
		if err != nil {
			return nil, err
		}
		for _, feature := range p.Descriptor.Features {
			if feature.Kind != "function" {
				return nil, fmt.Errorf("plugin.%s.feature.%s: %s is not enabled by this host version; use a function-only package", config.Name, feature.Name, feature.Kind)
			}
		}
		packages[i] = p
	}
	parent := filepath.Join(dir, ".terragraph", "plugins", "work")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	e.workDir, err = os.MkdirTemp(parent, "evaluation-")
	if err != nil {
		return nil, err
	}
	for i, config := range configs {
		work := filepath.Join(e.workDir, config.Name)
		if err := os.Mkdir(work, 0700); err != nil {
			return nil, err
		}
		pluginSettings := settings
		pluginSettings.alias = config.Name
		session, err := Open(context.WithValue(ctx, loggingKey{}, pluginSettings), packages[i], work, nil)
		if err != nil {
			return nil, fmt.Errorf("plugin.%s: %w", config.Name, err)
		}
		e.sessions = append(e.sessions, session)
		if _, err := session.Call(ctx, sdk.Request{Action: "configure", Config: config.Config}, 10*time.Second); err != nil {
			return nil, fmt.Errorf("plugin.%s.configure: %w", config.Name, err)
		}
		for _, feature := range packages[i].Descriptor.Features {
			name := config.Name + "_" + feature.Name
			if _, exists := e.Context.Functions[name]; exists {
				return nil, fmt.Errorf("plugin function %s is declared twice; change a plugin alias", name)
			}
			e.Context.Functions[name] = staticFunction(ctx, session, feature)
		}
	}
	return e, nil
}

func staticFunction(ctx context.Context, session *Session, feature sdk.Feature) function.Function {
	resultType, _ := ctyjson.UnmarshalType(feature.ResultType)
	params := make([]function.Parameter, len(feature.Parameters))
	for i, p := range feature.Parameters {
		t, _ := ctyjson.UnmarshalType(p.Type)
		params[i] = function.Parameter{Name: p.Name, Type: t, AllowNull: true}
	}
	return function.New(&function.Spec{Params: params, Type: function.StaticReturnType(resultType), Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		values := make([]sdk.Value, len(args))
		for i, arg := range args {
			var err error
			values[i], err = sdk.EncodeValue(arg, false)
			if err != nil {
				return cty.NilVal, fmt.Errorf("invalid function argument")
			}
		}
		response, err := session.Call(ctx, sdk.Request{Action: "function", Feature: feature.Name, Arguments: values}, 2*time.Second)
		if err != nil {
			return cty.NilVal, err
		}
		if response.Value == nil || response.Value.Sensitive {
			return cty.NilVal, fmt.Errorf("plugin function must return a non-sensitive value")
		}
		value, err := response.Value.Cty()
		if err != nil || !value.IsWhollyKnown() || !value.Type().Equals(resultType) {
			return cty.NilVal, fmt.Errorf("plugin function returned an invalid value or type")
		}
		return value, nil
	}})
}

// Close removes transient package working files after terminating every static session.
func (e *Evaluation) Close() error {
	for _, session := range e.sessions {
		session.Close()
	}
	if e.workDir != "" {
		if err := os.RemoveAll(e.workDir); err != nil {
			return fmt.Errorf("plugin cleanup %s: %w; remove the directory after confirming no invocation uses it", e.workDir, err)
		}
		e.workDir = ""
	}
	return nil
}
