package secretsmanager

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/cloudfluent/terragraph/plugin"
)

// TestDescriptor_ValidatesSdkContract guards the B0 decision: both features are read_only with no lifecycle events, so the host may retry API calls without durable execution records.
func TestDescriptor_ValidatesSdkContract(t *testing.T) {
	descriptor := Descriptor()
	if err := descriptor.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	if descriptor.Name != "secretsmanager" || descriptor.Version != "0.1.0" || descriptor.Protocol != sdk.ProtocolVersion {
		t.Fatalf("descriptor identity = %s %s protocol %d, want secretsmanager 0.1.0 protocol %d", descriptor.Name, descriptor.Version, descriptor.Protocol, sdk.ProtocolVersion)
	}
	if len(descriptor.Features) != 2 {
		t.Fatalf("len(Features) = %d, want 2", len(descriptor.Features))
	}
	for _, feature := range descriptor.Features {
		if feature.Effect != "read_only" {
			t.Fatalf("feature %s effect = %q, want read_only", feature.Name, feature.Effect)
		}
		if len(feature.Events) != 0 {
			t.Fatalf("feature %s events = %v, want none; input resolvers and credential providers receive no lifecycle events", feature.Name, feature.Events)
		}
	}
	if descriptor.Features[0].Name != "read" || descriptor.Features[0].Kind != "input_resolver" {
		t.Fatalf("first feature = %s/%s, want read/input_resolver", descriptor.Features[0].Name, descriptor.Features[0].Kind)
	}
	if descriptor.Features[1].Name != "authenticate" || descriptor.Features[1].Kind != "credential_provider" {
		t.Fatalf("second feature = %s/%s, want authenticate/credential_provider", descriptor.Features[1].Name, descriptor.Features[1].Kind)
	}
}

func TestHandler_ConfigureAcceptsRegionAndEndpoint(t *testing.T) {
	handler := Handler()
	_, err := handler(context.Background(), sdk.Request{Action: "configure", Config: map[string]any{"region": "us-east-1", "endpoint": "http://localhost:4566"}})
	if err != nil {
		t.Fatalf("configure error = %v, want nil", err)
	}
}

func TestHandler_ConfigureRequiresRegion(t *testing.T) {
	handler := Handler()
	_, err := handler(context.Background(), sdk.Request{Action: "configure", Config: map[string]any{"endpoint": "http://localhost:4566"}})
	if err == nil {
		t.Fatalf("configure error = nil, want missing region rejection")
	}
	if !strings.Contains(err.Error(), "region") || !strings.Contains(err.Error(), "required") {
		t.Fatalf("got = %v, want error naming region as required", err)
	}
}

func TestHandler_ConfigureRejectsUnknownKey(t *testing.T) {
	handler := Handler()
	_, err := handler(context.Background(), sdk.Request{Action: "configure", Config: map[string]any{"region": "us-east-1", "regions": "us-west-2"}})
	if err == nil {
		t.Fatalf("configure error = nil, want unknown key rejection")
	}
	if !strings.Contains(err.Error(), "regions") || !strings.Contains(err.Error(), "allowed keys are region and endpoint") {
		t.Fatalf("got = %v, want error naming the unknown key and the allowed keys", err)
	}
}

func TestHandler_ConfigureRejectsNonStringValue(t *testing.T) {
	handler := Handler()
	_, err := handler(context.Background(), sdk.Request{Action: "configure", Config: map[string]any{"region": 3}})
	if err == nil {
		t.Fatalf("configure error = nil, want type rejection")
	}
	if !strings.Contains(err.Error(), "region must be a string") {
		t.Fatalf("got = %v, want error naming region must be a string", err)
	}
}

func TestHandler_ReadNotImplemented(t *testing.T) {
	handler := Handler()
	_, err := handler(context.Background(), sdk.Request{Action: "read"})
	if err == nil {
		t.Fatalf("read error = nil, want not-implemented error instead of fake success")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("got = %v, want error saying read is not implemented", err)
	}
}

func TestHandler_AuthenticateNotImplemented(t *testing.T) {
	handler := Handler()
	_, err := handler(context.Background(), sdk.Request{Action: "authenticate"})
	if err == nil {
		t.Fatalf("authenticate error = nil, want not-implemented error instead of fake success")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("got = %v, want error saying authenticate is not implemented", err)
	}
}
