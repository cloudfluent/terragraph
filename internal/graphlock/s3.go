package graphlock

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// s3Backend owns a lock object via conditional PutObject (If-None-Match: *), the same
// idea as Terraform 1.10+ use_lockfile. Not S3 Object Lock (WORM). The object key is
// the graph identity from the blueprint; it must not be a node's state key.
type s3Backend struct {
	client *s3.Client
	bucket string
	key    string
}

func newS3Backend(cfg *blueprint.LockS3) (*s3Backend, error) {
	if cfg == nil {
		return nil, fmt.Errorf("lock.s3: nil config")
	}
	if cfg.Bucket == "" || cfg.Key == "" || cfg.Region == "" {
		return nil, fmt.Errorf("lock.s3: bucket, key, and region are required")
	}
	awsCfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("lock.s3: loading aws config: %w", err)
	}
	return &s3Backend{
		client: s3.NewFromConfig(awsCfg),
		bucket: cfg.Bucket,
		key:    cfg.Key,
	}, nil
}

func (b *s3Backend) TryCreate(ctx context.Context, body []byte) error {
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(b.key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json"),
		// Create only when absent: two runners racing both see success only if this is conditional.
		IfNoneMatch: aws.String("*"),
	})
	if err == nil {
		return nil
	}
	if isPreconditionFailed(err) {
		return fmt.Errorf("%w: s3://%s/%s", ErrHeld, b.bucket, b.key)
	}
	return fmt.Errorf("lock.s3: putting s3://%s/%s: %w", b.bucket, b.key, err)
}

func (b *s3Backend) Delete(ctx context.Context) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.key),
	})
	if err != nil {
		return fmt.Errorf("lock.s3: deleting s3://%s/%s: %w", b.bucket, b.key, err)
	}
	return nil
}

func isPreconditionFailed(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if code == "PreconditionFailed" || code == "ConditionalRequestConflict" {
			return true
		}
	}
	// Some SDK paths only surface the HTTP status in the message.
	msg := err.Error()
	return strings.Contains(msg, "PreconditionFailed") || strings.Contains(msg, "StatusCode: 412")
}
