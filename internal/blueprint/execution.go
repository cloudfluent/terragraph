package blueprint

import (
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// ExecutionConfig keeps journal storage independent of Terraform state and makes retained plans explicitly time-bounded.
type ExecutionConfig struct {
	Bucket          string
	Prefix          string
	Region          string
	PlanTTL         time.Duration
	RecordRetention time.Duration
}

var executionSchema = &hcl.BodySchema{Attributes: []hcl.AttributeSchema{
	{Name: "bucket"}, {Name: "prefix"}, {Name: "region"}, {Name: "plan_ttl"}, {Name: "record_retention"},
}}

func (b *Blueprint) ExecutionSettings() ExecutionConfig {
	if b != nil && b.Execution != nil {
		return *b.Execution
	}
	return ExecutionConfig{PlanTTL: 24 * time.Hour, RecordRetention: 30 * 24 * time.Hour}
}

func parseExecutionBlock(block *hcl.Block) (*ExecutionConfig, error) {
	content, diags := block.Body.Content(executionSchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("execution: %w", diags)
	}
	cfg := (&Blueprint{}).ExecutionSettings()
	values := map[string]string{}
	for name, attr := range content.Attributes {
		value, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || value.IsNull() || !value.IsKnown() || value.Type() != cty.String || value.AsString() == "" {
			return nil, fmt.Errorf("%s: execution.%s must be a non-empty literal string", attr.Range, name)
		}
		values[name] = value.AsString()
	}
	cfg.Bucket, cfg.Prefix, cfg.Region = values["bucket"], values["prefix"], values["region"]
	if cfg.Bucket != "" || cfg.Prefix != "" || cfg.Region != "" {
		if cfg.Bucket == "" || cfg.Prefix == "" || cfg.Region == "" {
			return nil, fmt.Errorf("execution: set bucket, prefix and region together for S3 storage")
		}
		cfg.Prefix = strings.Trim(cfg.Prefix, "/")
		if cfg.Prefix == "" || strings.ContainsAny(cfg.Bucket, "/\\:") || strings.Contains(cfg.Prefix, "\\") {
			return nil, fmt.Errorf("execution: use a bucket name and a dedicated non-root prefix")
		}
		for _, part := range strings.Split(cfg.Prefix, "/") {
			if part == "" || part == "." || part == ".." {
				return nil, fmt.Errorf("execution.prefix: remove empty or relative path components")
			}
		}
	}
	for _, name := range []string{"plan_ttl", "record_retention"} {
		if raw, ok := values[name]; ok {
			duration, err := time.ParseDuration(raw)
			if err != nil {
				return nil, fmt.Errorf("execution.%s: use a duration such as 24h: %w", name, err)
			}
			if duration <= 0 {
				return nil, fmt.Errorf("execution.%s: use a positive duration", name)
			}
			if name == "plan_ttl" {
				cfg.PlanTTL = duration
			} else {
				cfg.RecordRetention = duration
			}
		}
	}
	return &cfg, nil
}

func validateExecutionConfig(bp *Blueprint) error {
	cfg := bp.ExecutionSettings()
	if cfg.Bucket == "" {
		return nil
	}
	if bp.Lock == nil || bp.Lock.S3 == nil {
		return fmt.Errorf("execution: S3 storage requires a shared lock block; configure the same graph lock on every executor")
	}
	if cfg.Bucket == bp.Lock.S3.Bucket && (bp.Lock.S3.Key == cfg.Prefix || strings.HasPrefix(bp.Lock.S3.Key, cfg.Prefix+"/")) {
		return fmt.Errorf("execution.prefix: contains the graph lock; use a dedicated prefix so artifact cleanup cannot remove the lock")
	}
	return nil
}
