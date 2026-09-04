package graphlock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// s3API is the subset of the S3 client used to own the lock object.
type s3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type s3Backend struct {
	newClient func(ctx context.Context, region string) (s3API, error)
}

func (b s3Backend) Matches(lock *blueprint.Lock) bool {
	return lock != nil && lock.S3 != nil
}

func (b s3Backend) Acquire(ctx context.Context, lock *blueprint.Lock) (Held, error) {
	cfg := lock.S3
	client, err := b.client(ctx, cfg.Region)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(lockObject{
		Who:     lockWho(),
		Created: time.Now().UTC().Format(time.RFC3339),
		Info:    "terragraph graph lock",
	})
	if err != nil {
		return nil, fmt.Errorf("encoding graph lock: %w", err)
	}

	out, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(cfg.Bucket),
		Key:         aws.String(cfg.Key),
		Body:        bytes.NewReader(payload),
		IfNoneMatch: aws.String("*"),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		if isHeld(err) {
			return nil, heldError(ctx, client, cfg)
		}
		return nil, fmt.Errorf("acquiring graph lock s3://%s/%s: %w", cfg.Bucket, cfg.Key, err)
	}

	etag := ""
	if out != nil && out.ETag != nil {
		etag = *out.ETag
	}
	if etag == "" {
		// Close needs If-Match; without an ETag every later delete is a no-op leak.
		_, delErr := client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(cfg.Bucket),
			Key:    aws.String(cfg.Key),
		})
		if delErr != nil {
			return nil, fmt.Errorf("acquiring graph lock s3://%s/%s: PutObject returned no ETag (cleanup delete: %w)", cfg.Bucket, cfg.Key, delErr)
		}
		return nil, fmt.Errorf("acquiring graph lock s3://%s/%s: PutObject returned no ETag", cfg.Bucket, cfg.Key)
	}
	return &s3Held{client: client, bucket: cfg.Bucket, key: cfg.Key, etag: etag}, nil
}

// Release deletes the lock object unconditionally (force-unlock path). Unlike Close there is no If-Match: the etag belongs to a run that no longer exists, and gating on it would make recovery impossible.
func (b s3Backend) Release(ctx context.Context, lock *blueprint.Lock) error {
	cfg := lock.S3
	client, err := b.client(ctx, cfg.Region)
	if err != nil {
		return err
	}
	if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(cfg.Key),
	}); err != nil {
		return fmt.Errorf("releasing graph lock s3://%s/%s: %w", cfg.Bucket, cfg.Key, err)
	}
	return nil
}

func (b s3Backend) client(ctx context.Context, region string) (s3API, error) {
	if b.newClient != nil {
		return b.newClient(ctx, region)
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return s3.NewFromConfig(awsCfg), nil
}

type lockObject struct {
	Who     string
	Created string
	Info    string
}

func lockWho() string {
	name := "terragraph"
	if cu, err := user.Current(); err == nil && cu.Username != "" {
		name = cu.Username
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return name
	}
	return name + "@" + host
}

// isHeld reports whether err is S3's conditional-write failure (object already
// exists). AWS returns PreconditionFailed/412, and concurrent If-None-Match
// puts can also return ConditionalRequestConflict/409.
func isHeld(err error) bool {
	return isPreconditionFailed(err) || isConflict(err)
}

func isPreconditionFailed(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorCode() == "PreconditionFailed" {
		return true
	}
	var httpErr interface{ HTTPStatusCode() int }
	return errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == http.StatusPreconditionFailed
}

func isConflict(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorCode() == "ConditionalRequestConflict" {
		return true
	}
	var httpErr interface{ HTTPStatusCode() int }
	return errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == http.StatusConflict
}

func heldError(ctx context.Context, client s3API, cfg *blueprint.S3Lock) error {
	who, created := readLockHolder(ctx, client, cfg)
	if who != "" && created != "" {
		return fmt.Errorf("%w (s3://%s/%s held by %s since %s)", ErrHeld, cfg.Bucket, cfg.Key, who, created)
	}
	return fmt.Errorf("%w (s3://%s/%s)", ErrHeld, cfg.Bucket, cfg.Key)
}

func readLockHolder(ctx context.Context, client s3API, cfg *blueprint.S3Lock) (who, created string) {
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(cfg.Key),
	})
	if err != nil || out == nil || out.Body == nil {
		return "", ""
	}
	defer func() { _ = out.Body.Close() }()
	var obj lockObject
	if json.NewDecoder(out.Body).Decode(&obj) != nil {
		return "", ""
	}
	return obj.Who, obj.Created
}

type s3Held struct {
	client      s3API
	bucket, key string
	etag        string
	closed      bool
}

func (h *s3Held) Close() error {
	if h == nil || h.closed {
		return nil
	}
	if h.etag == "" {
		return fmt.Errorf("graph lock s3://%s/%s: missing ETag, cannot release", h.bucket, h.key)
	}
	_, err := h.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket:  aws.String(h.bucket),
		Key:     aws.String(h.key),
		IfMatch: aws.String(h.etag),
	})
	if err != nil {
		if isPreconditionFailed(err) {
			// If-Match missed: another writer replaced the object. We no longer own it.
			h.closed = true
			return nil
		}
		return err
	}
	h.closed = true
	return nil
}
