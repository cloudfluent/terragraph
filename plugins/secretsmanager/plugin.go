// Package secretsmanager implements the AWS Secrets Manager input resolver and credential provider on the public plugin SDK; it reads secrets over the AWS API and never persists values or credentials.
package secretsmanager

import (
	"context"
	"fmt"
	"runtime"

	sdk "github.com/cloudfluent/terragraph/plugin"
)

// Descriptor uses read_only effects because GetSecretValue and STS AssumeRole are idempotent reads; idempotent_external would additionally require durable execution records in enforce mode.
func Descriptor() sdk.Descriptor {
	executable := "terragraph-plugin-secretsmanager"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	return sdk.Descriptor{Name: "secretsmanager", Version: "0.1.0", Protocol: sdk.ProtocolVersion, Executable: executable, Features: []sdk.Feature{
		{Name: "read", Kind: "input_resolver", Effect: "read_only"},
		{Name: "authenticate", Kind: "credential_provider", Effect: "read_only"},
	}}
}

// Handler validates configuration eagerly so a bad plugin block fails at validate time, not mid-apply when the AWS call would first see it.
func Handler() sdk.Handler {
	h := &handler{}
	return func(ctx context.Context, request sdk.Request) (sdk.Response, error) {
		switch request.Action {
		case "configure":
			return h.configure(request.Config)
		case "read", "resolve":
			// resolve is the action the host lifecycle actually sends for input resolvers; read stays as the direct-call alias.
			return h.resolve(ctx, request.Reference)
		case "authenticate":
			return sdk.Response{}, fmt.Errorf("secretsmanager authenticate is not implemented yet; the STS credential lease lands in the next change")
		default:
			return sdk.Response{}, fmt.Errorf("unsupported secretsmanager action %q; use configure, read, or authenticate", request.Action)
		}
	}
}

// handler carries the accepted configure state between calls because the host sends Config only on configure, never on resolve.
type handler struct {
	region   string
	endpoint string
}

// configure stores region and endpoint for every later resolve on the same plugin process.
func (h *handler) configure(config map[string]any) (sdk.Response, error) {
	for key, value := range config {
		if key != "region" && key != "endpoint" {
			return sdk.Response{}, fmt.Errorf("unknown secretsmanager configuration key %q; allowed keys are region and endpoint", key)
		}
		if _, ok := value.(string); !ok {
			return sdk.Response{}, fmt.Errorf("secretsmanager configuration key %s must be a string", key)
		}
	}
	if region, ok := config["region"]; !ok || region == "" {
		return sdk.Response{}, fmt.Errorf("secretsmanager configuration key \"region\" is required; set it to the AWS region the secrets live in")
	} else {
		h.region, _ = region.(string)
	}
	if endpoint, ok := config["endpoint"]; ok {
		h.endpoint, _ = endpoint.(string)
	}
	return sdk.Response{}, nil
}
