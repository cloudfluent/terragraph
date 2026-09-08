package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// s3ExecutionStore uses conditional writes in addition to the graph lock so stale journals cannot overwrite newer decisions.
type s3ExecutionStore struct {
	client *s3.Client
	bucket string
	prefix string
}

var (
	_ executionStore = (*localExecutionStore)(nil)
	_ executionStore = (*s3ExecutionStore)(nil)
)

func openS3ExecutionStore(ctx context.Context, bucket, prefix, region string) (*s3ExecutionStore, error) {
	if bucket == "" || region == "" || strings.Trim(prefix, "/") == "" {
		return nil, fmt.Errorf("execution storage needs bucket, prefix and region; configure a dedicated prefix")
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("loading execution storage credentials: %w", err)
	}
	return &s3ExecutionStore{client: s3.NewFromConfig(cfg), bucket: bucket, prefix: strings.Trim(prefix, "/") + "/"}, nil
}

func (s *s3ExecutionStore) close() error { return nil }

func executionS3Error(err error) error {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return errExecutionMissing
		case "PreconditionFailed", "ConditionalRequestConflict":
			return errExecutionConflict
		}
	}
	return err
}

func (s *s3ExecutionStore) read(ctx context.Context, key string) (executionObject, error) {
	if err := checkExecutionKey(key); err != nil {
		return executionObject{}, err
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key)})
	if err != nil {
		return executionObject{}, executionS3Error(err)
	}
	defer func() { _ = result.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(result.Body, executionObjectLimit+1))
	if err != nil {
		return executionObject{}, err
	}
	if len(data) > executionObjectLimit {
		return executionObject{}, fmt.Errorf("execution object exceeds size limit; inspect the stored object")
	}
	var obj executionObject
	if err := json.Unmarshal(data, &obj); err != nil {
		return executionObject{}, fmt.Errorf("decoding execution object: %w", err)
	}
	if obj.Revision == "" || aws.ToString(result.ETag) == "" {
		return executionObject{}, fmt.Errorf("execution object has no revision; restore a valid record")
	}
	obj.Revision = aws.ToString(result.ETag)
	return obj, nil
}

func (s *s3ExecutionStore) write(ctx context.Context, key string, data []byte, revision string) (string, error) {
	if err := checkExecutionKey(key); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(executionObject{Revision: newExecutionID("rev"), Data: data})
	if err != nil {
		return "", err
	}
	if len(encoded) > executionObjectLimit {
		return "", fmt.Errorf("execution object exceeds size limit; reduce the plan size")
	}
	input := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key), Body: bytes.NewReader(encoded), ContentType: aws.String("application/json")}
	if revision == "" {
		input.IfNoneMatch = aws.String("*")
	} else {
		input.IfMatch = aws.String(revision)
	}
	result, err := s.client.PutObject(ctx, input)
	if err != nil {
		return "", executionS3Error(err)
	}
	if aws.ToString(result.ETag) == "" {
		return "", fmt.Errorf("execution storage returned no revision; inspect the object before retrying")
	}
	return aws.ToString(result.ETag), nil
}

func (s *s3ExecutionStore) remove(ctx context.Context, key, revision string) error {
	if err := checkExecutionKey(key); err != nil {
		return err
	}
	if revision == "" {
		return errExecutionConflict
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key), IfMatch: aws.String(revision)})
	return executionS3Error(err)
}

func (s *s3ExecutionStore) list(ctx context.Context) ([]string, error) {
	keys := []string{}
	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(s.prefix)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, executionS3Error(err)
		}
		for _, obj := range page.Contents {
			key := strings.TrimPrefix(aws.ToString(obj.Key), s.prefix)
			if executionKeyPattern.MatchString(key) {
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	return keys, nil
}
