// Command echo is a buildable example of the public plugin SDK, not a Terraform module or an engine hook.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/cloudfluent/terragraph/plugin"
)

func main() {
	executable := "plugin-echo"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	descriptor := plugin.Descriptor{Name: "echo", Version: "1.0.0", Protocol: plugin.ProtocolVersion, Executable: executable, Features: []plugin.Feature{{
		Name: "value", Kind: "function", Effect: "pure", Parameters: []plugin.Parameter{{Name: "value", Type: json.RawMessage(`"string"`)}}, ResultType: json.RawMessage(`"string"`),
	}}}
	if len(os.Args) == 2 && os.Args[1] == "--descriptor" {
		if err := json.NewEncoder(os.Stdout).Encode(descriptor); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	plugin.Serve(descriptor, func(ctx context.Context, request plugin.Request) (plugin.Response, error) {
		if request.Action == "configure" {
			return plugin.Response{}, nil
		}
		if request.Action != "function" || request.Feature != "value" || len(request.Arguments) != 1 {
			return plugin.Response{}, fmt.Errorf("unsupported request")
		}
		plugin.Logger(ctx).Info("returning input", "argument_count", len(request.Arguments))
		return plugin.Response{Value: &request.Arguments[0]}, nil
	})
}
