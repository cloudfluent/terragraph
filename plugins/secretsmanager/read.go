package secretsmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
	sdk "github.com/cloudfluent/terragraph/plugin"
	"github.com/zclconf/go-cty/cty"
)

// acceptedRefKeys is the closed reference vocabulary; rejecting unknown keys here keeps an HCL typo from silently reading the current version instead of erroring.
const acceptedRefKeys = "secret_id, version_id, version_stage, json_pointer, and field"

// secretRef is one parsed input reference; versionStage defaults to AWSCURRENT so an unpinned read always observes the same value the AWS console would show.
type secretRef struct {
	secretID     string
	versionID    string
	versionStage string
	jsonPointer  string
	field        string
}

// parseRef is the trust boundary for reference maps: values must be strings because HCL could hand over any cty type.
func parseRef(reference map[string]any) (secretRef, error) {
	ref := secretRef{versionStage: "AWSCURRENT"}
	for key, value := range reference {
		text, ok := value.(string)
		if !ok {
			return secretRef{}, fmt.Errorf("plugin.secretsmanager.read: reference key %s must be a string", key)
		}
		switch key {
		case "secret_id":
			ref.secretID = text
		case "version_id":
			ref.versionID = text
		case "version_stage":
			ref.versionStage = text
		case "json_pointer":
			ref.jsonPointer = text
		case "field":
			ref.field = text
		default:
			return secretRef{}, fmt.Errorf("plugin.secretsmanager.read: unknown reference key %q; allowed keys are %s", key, acceptedRefKeys)
		}
	}
	if ref.secretID == "" {
		return secretRef{}, fmt.Errorf("plugin.secretsmanager.read: reference key \"secret_id\" is required; set it to the secret name or full ARN to fetch")
	}
	if ref.versionStage == "" {
		ref.versionStage = "AWSCURRENT"
	}
	if ref.jsonPointer != "" && ref.field != "" {
		return secretRef{}, fmt.Errorf("plugin.secretsmanager.read: json_pointer and field are mutually exclusive; remove one so a single selection rule remains")
	}
	return ref, nil
}

// resolve fetches one secret over GetSecretValue; it logs nothing because the value and the AWS error detail both travel through this function.
func (h *handler) resolve(ctx context.Context, reference map[string]any) (sdk.Response, error) {
	ref, err := parseRef(reference)
	if err != nil {
		return sdk.Response{}, err
	}
	if h.region == "" {
		return sdk.Response{}, fmt.Errorf("plugin.secretsmanager.read: region is not configured; set region in the plugin secretsmanager block before reading secrets")
	}
	output, err := h.fetch(ctx, ref)
	if err != nil {
		// Only transport outcomes become faults: payload problems below are deterministic user-facing remedies, and a fatal fault would tear down the whole plugin session for one bad secret.
		return awsFault(err), nil
	}
	value, err := payloadText(output, ref.secretID)
	if err != nil {
		return sdk.Response{}, err
	}
	selected, err := selectProperty(value, ref)
	if err != nil {
		return sdk.Response{}, err
	}
	encoded, err := sdk.EncodeValue(cty.StringVal(selected), true)
	if err != nil {
		return sdk.Response{}, fmt.Errorf("plugin.secretsmanager.read: encoding the selected value: %w", err)
	}
	return sdk.Response{Value: &encoded}, nil
}

// fetch builds a client per call from the standard external chain (env, shared config, IAM role — never persisted by this plugin) so configure cannot pin stale credentials; only transport errors leave this function, so awsFault never sees a deterministic payload problem.
func (h *handler) fetch(ctx context.Context, ref secretRef) (*secretsmanager.GetSecretValueOutput, error) {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(h.region)}
	if h.endpoint != "" {
		// BaseEndpoint is the localstack escape hatch; without it the resolver would sign against the real AWS partition during tests.
		options = append(options, awsconfig.WithBaseEndpoint(h.endpoint))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	client := secretsmanager.NewFromConfig(cfg)
	input := &secretsmanager.GetSecretValueInput{SecretId: aws.String(ref.secretID)}
	// Exactly one version selector reaches the API: an explicit version_id pins an immutable version, otherwise the stage (default AWSCURRENT) chooses.
	if ref.versionID != "" {
		input.VersionId = aws.String(ref.versionID)
	} else {
		input.VersionStage = aws.String(ref.versionStage)
	}
	return client.GetSecretValue(ctx, input)
}

// payloadText fails with remedies rather than faults because v1 resolves only text secrets; these are properties of the stored secret, not of the AWS call.
func payloadText(output *secretsmanager.GetSecretValueOutput, secretID string) (string, error) {
	switch {
	case output.SecretString != nil:
		return *output.SecretString, nil
	case output.SecretBinary != nil:
		return "", fmt.Errorf("plugin.secretsmanager.read: secret %s stores a binary payload; store it as SecretString JSON instead, binary secrets are unsupported in v1", secretID)
	default:
		return "", fmt.Errorf("plugin.secretsmanager.read: secret %s returned neither a string nor a binary payload; inspect it in the AWS console", secretID)
	}
}

// awsFault maps AWS errors to safe fault codes; it must not copy AWS error strings, which can embed request identifiers and payloads, into anything the host persists.
func awsFault(err error) sdk.Response {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch code := apiErr.ErrorCode(); {
		case code == "ResourceNotFoundException":
			return fault("not_found", false, false)
		case code == "AccessDeniedException" || code == "UnrecognizedClientException":
			return fault("access_denied", false, false)
		case code == "ThrottlingException" || code == "SlowDown" || strings.Contains(code, "Throttling"):
			// read_only effect means the host may safely retry the whole resolve after backoff.
			return fault("throttled", false, true)
		}
	}
	return fault("aws_request_failed", true, false)
}

// fault builds the one fault shape this plugin emits; codes are snake_case because the host rejects anything outside ^[a-z][a-z0-9_]{0,63}$.
func fault(code string, fatal, retryable bool) sdk.Response {
	return sdk.Response{Fault: &sdk.Fault{Code: code, Fatal: fatal, Retryable: retryable}}
}

// selectProperty returns the whole SecretString when no selector is given (non-JSON secrets must still resolve), otherwise the selected JSON node rendered as text.
func selectProperty(secret string, ref secretRef) (string, error) {
	if ref.field == "" && ref.jsonPointer == "" {
		return secret, nil
	}
	doc := json.RawMessage(secret)
	var node json.RawMessage
	if ref.field != "" {
		selected, err := topLevelKey(doc, ref.field)
		if err != nil {
			return "", err
		}
		node = selected
	} else {
		selected, err := resolvePointer(doc, ref.jsonPointer)
		if err != nil {
			return "", err
		}
		node = selected
	}
	return render(node), nil
}

// topLevelKey fetches one top-level JSON key because field is the shorthand for the overwhelmingly common {"key": "value"} secret shape.
func topLevelKey(doc json.RawMessage, key string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(doc, &object); err != nil {
		return nil, fmt.Errorf("plugin.secretsmanager.read: field %q needs a JSON secret; store the value as a JSON object or drop field", key)
	}
	selected, ok := object[key]
	if !ok {
		return nil, fmt.Errorf("plugin.secretsmanager.read: field %q not found at the top level of the secret; fix the key or add it to the secret JSON", key)
	}
	return selected, nil
}

// resolvePointer walks an RFC 6901 pointer (~0/~1 unescaped) over the parsed document, refusing to descend into scalars.
func resolvePointer(doc json.RawMessage, pointer string) (json.RawMessage, error) {
	if pointer == "" {
		return doc, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("plugin.secretsmanager.read: json_pointer %q must start with '/' per RFC 6901; fix the pointer", pointer)
	}
	current := doc
	for _, token := range strings.Split(pointer[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		next, err := descend(current, token, pointer)
		if err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

func descend(current json.RawMessage, token, pointer string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(current)
	switch {
	case len(trimmed) > 0 && trimmed[0] == '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			return nil, fmt.Errorf("plugin.secretsmanager.read: json_pointer %q needs a JSON secret; store the value as JSON or drop the pointer", pointer)
		}
		next, ok := object[token]
		if !ok {
			return nil, fmt.Errorf("plugin.secretsmanager.read: json_pointer %q: key %q not found; fix the pointer or add the key to the secret JSON", pointer, token)
		}
		return next, nil
	case len(trimmed) > 0 && trimmed[0] == '[':
		var array []json.RawMessage
		if err := json.Unmarshal(current, &array); err != nil {
			return nil, fmt.Errorf("plugin.secretsmanager.read: json_pointer %q needs a JSON secret; store the value as JSON or drop the pointer", pointer)
		}
		index, err := strconv.Atoi(token)
		if err != nil || index < 0 {
			return nil, fmt.Errorf("plugin.secretsmanager.read: json_pointer %q: %q is not a valid array index; use the element position", pointer, token)
		}
		if index >= len(array) {
			return nil, fmt.Errorf("plugin.secretsmanager.read: json_pointer %q: index %d is out of bounds for %d elements; fix the pointer", pointer, index, len(array))
		}
		return array[index], nil
	default:
		return nil, fmt.Errorf("plugin.secretsmanager.read: json_pointer %q cannot descend into a scalar at %q; point at an object key or array index", pointer, token)
	}
}

// render keeps selected strings verbatim and renders numbers, bools, and containers as their JSON text so exact number formatting survives.
func render(node json.RawMessage) string {
	var text string
	if err := json.Unmarshal(node, &text); err == nil {
		return text
	}
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, node); err == nil {
		return compact.String()
	}
	return string(bytes.TrimSpace(node))
}
