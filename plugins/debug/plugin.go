// Package debug implements the first-party lifecycle observer using only the public plugin SDK; it never inspects input values, plan content, credentials, or module files.
package debug

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"

	sdk "github.com/cloudfluent/terragraph/plugin"
)

// Descriptor uses the same packaging and feature contract as a third-party executable.
func Descriptor() sdk.Descriptor {
	executable := "terragraph-plugin-debug"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	return sdk.Descriptor{Name: "debug", Version: "0.1.0", Protocol: sdk.ProtocolVersion, Executable: executable, Features: []sdk.Feature{{Name: "lifecycle", Kind: "observer", Effect: "pure", Events: append([]string(nil), sdk.LifecyclePhases...)}}}
}

// Handler returns one independently configured observer instance; log severity never changes execution policy.
func Handler() sdk.Handler {
	level := slog.LevelDebug
	return func(ctx context.Context, request sdk.Request) (sdk.Response, error) {
		switch request.Action {
		case "configure":
			for key := range request.Config {
				if key != "level" {
					return sdk.Response{}, fmt.Errorf("unknown debug configuration key")
				}
			}
			if value, ok := request.Config["level"]; ok {
				text, ok := value.(string)
				if !ok {
					return sdk.Response{}, fmt.Errorf("level must be a string")
				}
				if err := level.UnmarshalText([]byte(text)); err != nil {
					return sdk.Response{}, fmt.Errorf("invalid log level")
				}
			}
			return sdk.Response{}, nil
		case "observer":
			event := request.Event
			sdk.Logger(ctx).Log(ctx, level, "terragraph lifecycle",
				"event_id", event.ID, "operation", event.Operation, "phase", event.Phase,
				"node", event.Node, "status", event.Status, "execution_id", event.ExecutionID,
				"selected_nodes", len(event.Nodes), "graph_nodes", len(event.Graph))
			return sdk.Response{}, nil
		default:
			return sdk.Response{}, fmt.Errorf("unsupported debug action")
		}
	}
}
