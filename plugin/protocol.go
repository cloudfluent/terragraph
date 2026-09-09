// Package plugin defines the versioned executable-plugin contract; it never schedules infrastructure or owns Terraform state.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

const ProtocolVersion = 3
const MaxMessageSize = 4 << 20

// Descriptor is inspectable without executing untrusted package code during discovery or editor completion.
type Descriptor struct {
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	Protocol   int       `json:"protocol"`
	Executable string    `json:"executable"`
	Features   []Feature `json:"features"`
}

// Feature limits which phases and effects a plugin can request rather than exposing a mutable engine.
type Feature struct {
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	Events     []string        `json:"events,omitempty"`
	Operations []string        `json:"operations,omitempty"`
	Effect     string          `json:"effect"`
	Parameters []Parameter     `json:"parameters,omitempty"`
	ResultType json.RawMessage `json:"result_type,omitempty"`
	Sensitive  bool            `json:"sensitive,omitempty"`
}

type Parameter struct {
	Name string          `json:"name"`
	Type json.RawMessage `json:"type"`
}

// Value preserves exact numbers, nulls and sensitivity across the process boundary.
type Value struct {
	Type      json.RawMessage `json:"type"`
	JSON      json.RawMessage `json:"value"`
	Sensitive bool            `json:"sensitive,omitempty"`
}

func EncodeValue(value cty.Value, sensitive bool) (Value, error) {
	t, err := ctyjson.MarshalType(value.Type())
	if err != nil {
		return Value{}, err
	}
	v, err := ctyjson.Marshal(value, value.Type())
	return Value{Type: t, JSON: v, Sensitive: sensitive}, err
}

func (v Value) Cty() (cty.Value, error) {
	t, err := ctyjson.UnmarshalType(v.Type)
	if err != nil {
		return cty.NilVal, fmt.Errorf("invalid plugin value type: %w", err)
	}
	return ctyjson.Unmarshal(v.JSON, t)
}

func (v Value) Decode() (any, error) {
	if _, err := v.Cty(); err != nil {
		return nil, err
	}
	var result any
	d := json.NewDecoder(bytes.NewReader(v.JSON))
	d.UseNumber()
	err := d.Decode(&result)
	return result, err
}

// Event contains immutable evidence, never a path granting write access to a native plan.
type Event struct {
	Graph       []GraphNode       `json:"graph,omitempty"`
	ID          string            `json:"id"`
	ExecutionID string            `json:"execution_id,omitempty"`
	Operation   string            `json:"operation"`
	Phase       string            `json:"phase"`
	Node        string            `json:"node,omitempty"`
	Nodes       []string          `json:"nodes,omitempty"`
	Status      string            `json:"status,omitempty"`
	PlanID      string            `json:"plan_id,omitempty"`
	Plan        json.RawMessage   `json:"plan,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Request struct {
	LogLevel       int            `json:"log_level"`
	ID             string         `json:"id"`
	Action         string         `json:"action"`
	Feature        string         `json:"feature,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
	Arguments      []Value        `json:"arguments,omitempty"`
	Reference      map[string]any `json:"reference,omitempty"`
	Event          Event          `json:"event"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	Lease          *Lease         `json:"lease,omitempty"`
}

// Fault carries branching metadata without copying provider errors that may contain secret payloads.
type Fault struct {
	Code      string `json:"code"`
	Fatal     bool   `json:"fatal,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

type Lease struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
	RenewAt   time.Time `json:"renew_at,omitempty"`
}

// Response separates business rejection from a broken plugin and cannot request infrastructure replay.
type Response struct {
	Identity       string            `json:"identity,omitempty"`
	ExpiresAt      time.Time         `json:"expires_at,omitempty"`
	Expansion      *Expansion        `json:"expansion,omitempty"`
	LogsIncomplete bool              `json:"logs_incomplete,omitempty"`
	Value          *Value            `json:"value,omitempty"`
	Decision       string            `json:"decision,omitempty"`
	Code           string            `json:"code,omitempty"`
	Version        string            `json:"version,omitempty"`
	Credentials    map[string]string `json:"credentials,omitempty"`
	Lease          *Lease            `json:"lease,omitempty"`
	Report         json.RawMessage   `json:"report,omitempty"`
	Fault          *Fault            `json:"fault,omitempty"`
}

type Handler func(context.Context, Request) (Response, error)
