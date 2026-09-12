package secretsmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// stubAWSEnv pins the default chain to static env credentials and one attempt so canned transport responses surface immediately; the plugin itself must keep using the standard chain.
func stubAWSEnv(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "absent")
	t.Setenv("AWS_ACCESS_KEY_ID", "testing")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "testing")
	t.Setenv("AWS_SESSION_TOKEN", "testing")
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	t.Setenv("AWS_CONFIG_FILE", empty)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", empty)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

// wireRecorder captures request bodies under a mutex because the server handler runs on another goroutine and the suite runs with -race.
type wireRecorder struct {
	mu     sync.Mutex
	bodies []string
}

func (r *wireRecorder) record(body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, body)
}

func (r *wireRecorder) last(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		t.Fatalf("fake secretsmanager received no requests")
	}
	var wire map[string]any
	if err := json.Unmarshal([]byte(r.bodies[len(r.bodies)-1]), &wire); err != nil {
		t.Fatalf("decoding recorded request body %q: %v", r.bodies[len(r.bodies)-1], err)
	}
	return wire
}

// fakeSecretsManager answers every GetSecretValue with the canned payload and records request bodies; this is what configure's endpoint option exists for.
func fakeSecretsManager(t *testing.T, status int, payload string) (string, *wireRecorder) {
	t.Helper()
	recorder := &wireRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		recorder.record(string(raw))
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	return server.URL, recorder
}

func configuredHandler(t *testing.T, endpoint string) sdk.Handler {
	t.Helper()
	handler := Handler()
	_, err := handler(context.Background(), sdk.Request{Action: "configure", Config: map[string]any{"region": "us-east-1", "endpoint": endpoint}})
	if err != nil {
		t.Fatalf("configure error = %v, want nil", err)
	}
	return handler
}

// resolveInput mirrors the host wire shape: the lifecycle sends action resolve with the HCL reference map.
func resolveInput(handler sdk.Handler, reference map[string]any) (sdk.Response, error) {
	return handler(context.Background(), sdk.Request{Action: "resolve", Feature: "read", Reference: reference})
}

func secretPayload(t *testing.T, secretString string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"Name": "prod/db", "VersionId": "v-1", "SecretString": secretString})
	if err != nil {
		t.Fatalf("marshaling fake payload: %v", err)
	}
	return string(payload)
}

const canary = "canary-value-7f3d2a"

var secretDoc = fmt.Sprintf(`{"password":%q,"retries":3,"flag":true,"nested":{"token":"nested-canary-9b1c"}}`, canary)

func TestHandler_ReadReturnsSensitiveStringValue(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	response, err := resolveInput(handler, map[string]any{"secret_id": "prod/db"})
	if err != nil {
		t.Fatalf("read error = %v, want nil", err)
	}
	if response.Fault != nil {
		t.Fatalf("read fault = %+v, want nil", response.Fault)
	}
	value := response.Value
	if value == nil {
		t.Fatalf("read value = nil, want a typed string value")
	}
	if string(value.Type) != `"string"` {
		t.Fatalf("value type = %s, want \"string\"", value.Type)
	}
	var decoded string
	if err := json.Unmarshal(value.JSON, &decoded); err != nil || decoded != secretDoc {
		t.Fatalf("value json = %s (%v), want the whole SecretString %q", value.JSON, err, secretDoc)
	}
	if !value.Sensitive {
		t.Fatalf("value sensitive = false, want true")
	}
}

func TestHandler_ReadSelectsJSONPointer(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	response, err := resolveInput(handler, map[string]any{"secret_id": "prod/db", "json_pointer": "/nested/token"})
	if err != nil {
		t.Fatalf("read error = %v, want nil", err)
	}
	var decoded string
	if err := json.Unmarshal(response.Value.JSON, &decoded); err != nil || decoded != "nested-canary-9b1c" {
		t.Fatalf("value json = %s (%v), want nested-canary-9b1c", response.Value.JSON, err)
	}
}

func TestHandler_ReadPointerOutOfBounds(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	_, err := resolveInput(handler, map[string]any{"secret_id": "prod/db", "json_pointer": "/nested/missing"})
	if err == nil {
		t.Fatalf("read error = nil, want out-of-bounds rejection")
	}
	if !strings.Contains(err.Error(), "json_pointer") || !strings.Contains(err.Error(), "/nested/missing") {
		t.Fatalf("got = %v, want error naming the pointer", err)
	}
}

func TestHandler_ReadSelectsField(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	response, err := resolveInput(handler, map[string]any{"secret_id": "prod/db", "field": "password"})
	if err != nil {
		t.Fatalf("read error = %v, want nil", err)
	}
	var decoded string
	if err := json.Unmarshal(response.Value.JSON, &decoded); err != nil || decoded != canary {
		t.Fatalf("value json = %s (%v), want %q", response.Value.JSON, err, canary)
	}
}

func TestHandler_ReadFieldRendersNumbersAsJSONText(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	response, err := resolveInput(handler, map[string]any{"secret_id": "prod/db", "field": "retries"})
	if err != nil {
		t.Fatalf("read error = %v, want nil", err)
	}
	var decoded string
	if err := json.Unmarshal(response.Value.JSON, &decoded); err != nil || decoded != "3" {
		t.Fatalf("value json = %s (%v), want number rendered as JSON text \"3\"", response.Value.JSON, err)
	}
}

func TestHandler_ReadPinsVersionID(t *testing.T) {
	stubAWSEnv(t)
	url, recorder := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	_, err := resolveInput(handler, map[string]any{"secret_id": "prod/db", "version_id": "v-2", "version_stage": "AWSPENDING"})
	if err != nil {
		t.Fatalf("read error = %v, want nil", err)
	}
	wire := recorder.last(t)
	if wire["SecretId"] != "prod/db" {
		t.Fatalf("wire SecretId = %v, want prod/db", wire["SecretId"])
	}
	if wire["VersionId"] != "v-2" {
		t.Fatalf("wire VersionId = %v, want v-2", wire["VersionId"])
	}
	if _, ok := wire["VersionStage"]; ok {
		t.Fatalf("wire body = %v, want no VersionStage when version_id pins the version", wire)
	}
}

func TestHandler_ReadDefaultsVersionStageToAWSCURRENT(t *testing.T) {
	stubAWSEnv(t)
	url, recorder := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	_, err := resolveInput(handler, map[string]any{"secret_id": "prod/db"})
	if err != nil {
		t.Fatalf("read error = %v, want nil", err)
	}
	wire := recorder.last(t)
	if wire["VersionStage"] != "AWSCURRENT" {
		t.Fatalf("wire VersionStage = %v, want default AWSCURRENT", wire["VersionStage"])
	}
	if _, ok := wire["VersionId"]; ok {
		t.Fatalf("wire body = %v, want no VersionId when only a stage is requested", wire)
	}
}

func TestHandler_ReadSendsExplicitVersionStage(t *testing.T) {
	stubAWSEnv(t)
	url, recorder := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	_, err := resolveInput(handler, map[string]any{"secret_id": "prod/db", "version_stage": "AWSPENDING"})
	if err != nil {
		t.Fatalf("read error = %v, want nil", err)
	}
	wire := recorder.last(t)
	if wire["VersionStage"] != "AWSPENDING" {
		t.Fatalf("wire VersionStage = %v, want AWSPENDING", wire["VersionStage"])
	}
}

func TestHandler_ReadMapsNotFoundFault(t *testing.T) {
	stubAWSEnv(t)
	probe := "leak-probe-4d71 Secrets Manager can't find prod/db"
	payload, _ := json.Marshal(map[string]any{"__type": "ResourceNotFoundException", "Message": probe})
	url, _ := fakeSecretsManager(t, http.StatusBadRequest, string(payload))
	handler := configuredHandler(t, url)
	response, err := resolveInput(handler, map[string]any{"secret_id": "prod/db"})
	if err != nil {
		t.Fatalf("read error = %v, want fault response with nil error", err)
	}
	if response.Fault == nil || response.Fault.Code != "not_found" {
		t.Fatalf("read fault = %+v, want code not_found", response.Fault)
	}
	if response.Fault.Fatal || response.Fault.Retryable {
		t.Fatalf("not_found fault = %+v, want non-fatal and non-retryable", response.Fault)
	}
	if serialized, _ := json.Marshal(response); strings.Contains(string(serialized), probe) {
		t.Fatalf("not-found response %s leaks the AWS error message", serialized)
	}
}

func TestHandler_ReadMapsAccessDeniedFault(t *testing.T) {
	stubAWSEnv(t)
	payload, _ := json.Marshal(map[string]any{"__type": "AccessDeniedException", "Message": "User: leak-probe is not authorized"})
	url, _ := fakeSecretsManager(t, http.StatusBadRequest, string(payload))
	handler := configuredHandler(t, url)
	response, err := resolveInput(handler, map[string]any{"secret_id": "prod/db"})
	if err != nil {
		t.Fatalf("read error = %v, want fault response with nil error", err)
	}
	if response.Fault == nil || response.Fault.Code != "access_denied" {
		t.Fatalf("read fault = %+v, want code access_denied", response.Fault)
	}
	if response.Fault.Fatal || response.Fault.Retryable {
		t.Fatalf("access_denied fault = %+v, want non-fatal and non-retryable", response.Fault)
	}
}

func TestHandler_ReadMapsThrottlingRetryable(t *testing.T) {
	stubAWSEnv(t)
	payload, _ := json.Marshal(map[string]any{"__type": "ThrottlingException", "Message": "Rate exceeded"})
	url, _ := fakeSecretsManager(t, http.StatusTooManyRequests, string(payload))
	handler := configuredHandler(t, url)
	response, err := resolveInput(handler, map[string]any{"secret_id": "prod/db"})
	if err != nil {
		t.Fatalf("read error = %v, want fault response with nil error", err)
	}
	if response.Fault == nil || response.Fault.Code != "throttled" {
		t.Fatalf("read fault = %+v, want code throttled", response.Fault)
	}
	if !response.Fault.Retryable || response.Fault.Fatal {
		t.Fatalf("throttled fault = %+v, want retryable and non-fatal", response.Fault)
	}
}

func TestHandler_ReadRejectsPointerAndField(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	_, err := resolveInput(handler, map[string]any{"secret_id": "prod/db", "json_pointer": "/password", "field": "password"})
	if err == nil {
		t.Fatalf("read error = nil, want mutual-exclusion rejection")
	}
	if !strings.Contains(err.Error(), "json_pointer") || !strings.Contains(err.Error(), "field") || !strings.Contains(err.Error(), "remove") {
		t.Fatalf("got = %v, want error naming both keys and the remove remedy", err)
	}
}

func TestHandler_ReadRequiresSecretID(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	_, err := resolveInput(handler, map[string]any{"field": "password"})
	if err == nil {
		t.Fatalf("read error = nil, want missing secret_id rejection")
	}
	if !strings.Contains(err.Error(), "secret_id") || !strings.Contains(err.Error(), "required") {
		t.Fatalf("got = %v, want error naming secret_id as required", err)
	}
}

func TestHandler_ReadRejectsUnknownRefKey(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	_, err := resolveInput(handler, map[string]any{"secret_id": "prod/db", "secretId": "prod/db"})
	if err == nil {
		t.Fatalf("read error = nil, want unknown key rejection")
	}
	if !strings.Contains(err.Error(), "secretId") || !strings.Contains(err.Error(), "secret_id") {
		t.Fatalf("got = %v, want error naming the unknown key and the accepted keys", err)
	}
}

func TestHandler_ReadRequiresConfiguredRegion(t *testing.T) {
	stubAWSEnv(t)
	handler := Handler()
	_, err := resolveInput(handler, map[string]any{"secret_id": "prod/db"})
	if err == nil {
		t.Fatalf("read error = nil, want unconfigured region rejection")
	}
	if !strings.Contains(err.Error(), "region") || !strings.Contains(err.Error(), "configure") {
		t.Fatalf("got = %v, want error naming the region configure remedy", err)
	}
}

// TestHandler_ReadValueNeverLeaks asserts the secret value exists only inside the typed value: nowhere else in the serialized response.
func TestHandler_ReadValueNeverLeaks(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSecretsManager(t, http.StatusOK, secretPayload(t, secretDoc))
	handler := configuredHandler(t, url)
	response, err := resolveInput(handler, map[string]any{"secret_id": "prod/db", "field": "password"})
	if err != nil {
		t.Fatalf("read error = %v, want nil", err)
	}
	serialized, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshaling response: %v", err)
	}
	total := strings.Count(string(serialized), canary)
	inside := strings.Count(string(response.Value.JSON), canary)
	if total != inside || total != 1 {
		t.Fatalf("canary appears %d times in response (%d in value), want exactly once inside the typed value: %s", total, inside, serialized)
	}
}

// stsRequest captures one signed STS call so tests assert wire-level facts: which action ran, what session name it carried, and which access key signed it.
type stsRequest struct {
	action string
	query  url.Values
	auth   string
}

// stsRecorder captures requests under a mutex because the server handler runs on another goroutine and the suite runs with -race.
type stsRecorder struct {
	mu       sync.Mutex
	requests []stsRequest
}

func (r *stsRecorder) add(req stsRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
}

func (r *stsRecorder) snapshot(t *testing.T) []stsRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stsRequest(nil), r.requests...)
}

// fakeSTS answers query-protocol STS calls with the test's canned XML keyed by action; authenticate crosses exactly this server via configure's endpoint.
func fakeSTS(t *testing.T, respond func(action string) (int, string)) (string, *stsRecorder) {
	t.Helper()
	recorder := &stsRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		query, err := url.ParseQuery(string(raw))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		recorder.add(stsRequest{action: query.Get("Action"), query: query, auth: r.Header.Get("Authorization")})
		status, payload := respond(query.Get("Action"))
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	return server.URL, recorder
}

// authenticateInput mirrors the host wire shape: the lifecycle sends action acquire with the HCL reference map for credential providers.
func authenticateInput(handler sdk.Handler, reference map[string]any) (sdk.Response, error) {
	return handler(context.Background(), sdk.Request{Action: "acquire", Feature: "authenticate", Reference: reference})
}

// credentialNames asserts the exact environment vocabulary because the host rejects any returned name outside the binding's declared allowlist.
func credentialNames(t *testing.T, env map[string]string, want ...string) {
	t.Helper()
	if len(env) != len(want) {
		t.Fatalf("credential env names = %v, want exactly %v", env, want)
	}
	for _, name := range want {
		if _, ok := env[name]; !ok {
			t.Fatalf("credential env names = %v, want exactly %v", env, want)
		}
	}
}

const (
	callerArn          = "arn:aws:iam::123456789012:user/rokhun"
	assumedRoleArn     = "arn:aws:sts::123456789012:assumed-role/deploy/terragraph-secretsmanager"
	roleArn            = "arn:aws:iam::123456789012:role/deploy"
	accessKeyCanary    = "ASIA-canary-access-5e1c"
	secretKeyCanary    = "canary-secret-key-4a7e"
	sessionTokenCanary = "canary-session-token-8d2f"
)

// callerIdentityXML pins one principal ARN so stability assertions compare a real canned string, not a re-derived one.
const callerIdentityXML = `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Arn>` + callerArn + `</Arn><UserId>AIDACK1234567890EXAMPLE</UserId><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`

// assumeRoleXML renders the STS AssumeRole result with canary credentials because the leak test needs known strings in the secret positions.
func assumeRoleXML(t *testing.T, expiration time.Time) string {
	t.Helper()
	return fmt.Sprintf(`<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><AssumedRoleUser><Arn>%s</Arn><AssumedRoleId>AROACK1234567890EXAMPLE:terragraph-secretsmanager</AssumedRoleId></AssumedRoleUser><Credentials><AccessKeyId>%s</AccessKeyId><SecretAccessKey>%s</SecretAccessKey><SessionToken>%s</SessionToken><Expiration>%s</Expiration></Credentials></AssumeRoleResult></AssumeRoleResponse>`, assumedRoleArn, accessKeyCanary, secretKeyCanary, sessionTokenCanary, expiration.Format(time.RFC3339))
}

// throttledXML is the query-protocol error shape; the Code element is what smithy lifts into ErrorCode.
const throttledXML = `<ErrorResponse><Error><Type>Sender</Type><Code>ThrottlingException</Code><Message>Rate exceeded</Message></Error></ErrorResponse>`

func TestHandler_AuthenticateStaticCredentials(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSTS(t, func(action string) (int, string) { return http.StatusOK, callerIdentityXML })
	handler := configuredHandler(t, url)
	response, err := authenticateInput(handler, map[string]any{})
	if err != nil {
		t.Fatalf("authenticate error = %v, want nil", err)
	}
	if response.Identity != callerArn {
		t.Fatalf("identity = %q, want the GetCallerIdentity ARN %q", response.Identity, callerArn)
	}
	credentialNames(t, response.Credentials, "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN")
	if response.Credentials["AWS_ACCESS_KEY_ID"] != "testing" || response.Credentials["AWS_SECRET_ACCESS_KEY"] != "testing" || response.Credentials["AWS_SESSION_TOKEN"] != "testing" {
		t.Fatalf("credentials = %v, want the stubbed static chain values", response.Credentials)
	}
	if response.Lease != nil {
		t.Fatalf("lease = %+v, want none: static chain credentials do not expire inside one run", response.Lease)
	}
}

func TestHandler_AuthenticateAcceptsAuthenticateAlias(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSTS(t, func(action string) (int, string) { return http.StatusOK, callerIdentityXML })
	handler := configuredHandler(t, url)
	response, err := handler(context.Background(), sdk.Request{Action: "authenticate", Feature: "authenticate", Reference: map[string]any{}})
	if err != nil {
		t.Fatalf("authenticate alias error = %v, want nil", err)
	}
	if response.Identity != callerArn {
		t.Fatalf("identity = %q, want %q", response.Identity, callerArn)
	}
}

func TestHandler_AuthenticateOmitsAbsentSessionToken(t *testing.T) {
	stubAWSEnv(t)
	t.Setenv("AWS_SESSION_TOKEN", "")
	url, _ := fakeSTS(t, func(action string) (int, string) { return http.StatusOK, callerIdentityXML })
	handler := configuredHandler(t, url)
	response, err := authenticateInput(handler, map[string]any{})
	if err != nil {
		t.Fatalf("authenticate error = %v, want nil", err)
	}
	credentialNames(t, response.Credentials, "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY")
}

func TestHandler_AuthenticateAssumeRole(t *testing.T) {
	stubAWSEnv(t)
	// STS exchanges the expiration as xsd:dateTime, whole seconds only, so the canned value must not carry nanoseconds the wire cannot round-trip.
	expiration := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	url, recorder := fakeSTS(t, func(action string) (int, string) {
		if action == "AssumeRole" {
			return http.StatusOK, assumeRoleXML(t, expiration)
		}
		return http.StatusOK, callerIdentityXML
	})
	handler := configuredHandler(t, url)
	response, err := authenticateInput(handler, map[string]any{"role_arn": roleArn})
	if err != nil {
		t.Fatalf("authenticate error = %v, want nil", err)
	}
	if response.Identity != assumedRoleArn {
		t.Fatalf("identity = %q, want the assumed-role ARN %q", response.Identity, assumedRoleArn)
	}
	credentialNames(t, response.Credentials, "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN")
	if response.Credentials["AWS_ACCESS_KEY_ID"] != accessKeyCanary || response.Credentials["AWS_SECRET_ACCESS_KEY"] != secretKeyCanary || response.Credentials["AWS_SESSION_TOKEN"] != sessionTokenCanary {
		t.Fatalf("credentials = %v, want the canned AssumeRole values", response.Credentials)
	}
	if response.Lease == nil || response.Lease.ID != assumedRoleArn || !response.Lease.ExpiresAt.Equal(expiration) {
		t.Fatalf("lease = %+v, want ID %q expiring at %s", response.Lease, assumedRoleArn, expiration)
	}
	if !response.Lease.RenewAt.IsZero() {
		t.Fatalf("lease RenewAt = %v, want zero: v1 cannot rotate credentials under a running subprocess", response.Lease.RenewAt)
	}
	for _, request := range recorder.snapshot(t) {
		if request.action == "GetCallerIdentity" {
			t.Fatalf("assume path issued %s; identity must come from the AssumeRole result itself", request.action)
		}
	}
}

func TestHandler_AuthenticateSessionNameReachesWire(t *testing.T) {
	stubAWSEnv(t)
	url, recorder := fakeSTS(t, func(action string) (int, string) {
		return http.StatusOK, assumeRoleXML(t, time.Now().Add(time.Hour))
	})
	handler := configuredHandler(t, url)
	_, err := authenticateInput(handler, map[string]any{"role_arn": roleArn, "session_name": "deploy-session"})
	if err != nil {
		t.Fatalf("authenticate error = %v, want nil", err)
	}
	requests := recorder.snapshot(t)
	if len(requests) == 0 {
		t.Fatalf("fake STS received no requests")
	}
	if got := requests[len(requests)-1].query.Get("RoleSessionName"); got != "deploy-session" {
		t.Fatalf("RoleSessionName = %q, want the requested session name deploy-session", got)
	}
}

func TestHandler_AuthenticateUsesSharedProfile(t *testing.T) {
	stubAWSEnv(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	credentialsFile := filepath.Join(t.TempDir(), "credentials")
	profile := "[staging]\naws_access_key_id = AKIDPROFILESTAGING\naws_secret_access_key = profile-secret\n"
	if err := os.WriteFile(credentialsFile, []byte(profile), 0o600); err != nil {
		t.Fatalf("writing shared credentials file: %v", err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsFile)
	t.Setenv("AWS_CONFIG_FILE", credentialsFile)
	url, recorder := fakeSTS(t, func(action string) (int, string) { return http.StatusOK, callerIdentityXML })
	handler := configuredHandler(t, url)
	response, err := authenticateInput(handler, map[string]any{"profile": "staging"})
	if err != nil {
		t.Fatalf("authenticate error = %v, want nil", err)
	}
	if response.Identity != callerArn {
		t.Fatalf("identity = %q, want %q", response.Identity, callerArn)
	}
	requests := recorder.snapshot(t)
	if len(requests) == 0 {
		t.Fatalf("fake STS received no requests")
	}
	if !strings.Contains(requests[len(requests)-1].auth, "Credential=AKIDPROFILESTAGING/") {
		t.Fatalf("request signed with %q, want the shared profile access key AKIDPROFILESTAGING", requests[len(requests)-1].auth)
	}
}

func TestHandler_AuthenticateIdentityStable(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSTS(t, func(action string) (int, string) { return http.StatusOK, callerIdentityXML })
	handler := configuredHandler(t, url)
	first, err := authenticateInput(handler, map[string]any{})
	if err != nil {
		t.Fatalf("first authenticate error = %v, want nil", err)
	}
	second, err := authenticateInput(handler, map[string]any{})
	if err != nil {
		t.Fatalf("second authenticate error = %v, want nil", err)
	}
	if first.Identity == "" || first.Identity != second.Identity {
		t.Fatalf("identity drifted between acquires: %q vs %q, want one stable non-empty string", first.Identity, second.Identity)
	}
}

func TestHandler_AuthenticateRequiresConfiguredRegion(t *testing.T) {
	stubAWSEnv(t)
	handler := Handler()
	_, err := authenticateInput(handler, map[string]any{})
	if err == nil {
		t.Fatalf("authenticate error = nil, want unconfigured region rejection")
	}
	if !strings.Contains(err.Error(), "region") || !strings.Contains(err.Error(), "configure") {
		t.Fatalf("got = %v, want error naming the region configure remedy", err)
	}
}

func TestHandler_AuthenticateRejectsUnknownRefKey(t *testing.T) {
	stubAWSEnv(t)
	handler := configuredHandler(t, "")
	_, err := authenticateInput(handler, map[string]any{"role": roleArn})
	if err == nil {
		t.Fatalf("authenticate error = nil, want unknown key rejection")
	}
	if !strings.Contains(err.Error(), "role") || !strings.Contains(err.Error(), acceptedAuthKeys) {
		t.Fatalf("got = %v, want error naming the unknown key and %s", err, acceptedAuthKeys)
	}
}

func TestHandler_AuthenticateThrottledMapsRetryable(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSTS(t, func(action string) (int, string) { return http.StatusBadRequest, throttledXML })
	handler := configuredHandler(t, url)
	response, err := authenticateInput(handler, map[string]any{})
	if err != nil {
		t.Fatalf("authenticate error = %v, want nil: faults travel in the response", err)
	}
	if response.Fault == nil || response.Fault.Code != "throttled" || !response.Fault.Retryable || response.Fault.Fatal {
		t.Fatalf("fault = %+v, want code throttled, retryable, non-fatal", response.Fault)
	}
}

func TestHandler_AuthenticateReleaseIsAccepted(t *testing.T) {
	handler := Handler()
	_, err := handler(context.Background(), sdk.Request{Action: "release", Feature: "authenticate", Lease: &sdk.Lease{ID: assumedRoleArn, ExpiresAt: time.Now().Add(time.Hour)}})
	if err != nil {
		t.Fatalf("release error = %v, want nil: the host releases every lease at runtime close", err)
	}
}

// TestHandler_AuthenticateSecretsNeverLeak asserts the secret key and session token exist only inside the typed credentials map: nowhere else in the serialized response.
func TestHandler_AuthenticateSecretsNeverLeak(t *testing.T) {
	stubAWSEnv(t)
	url, _ := fakeSTS(t, func(action string) (int, string) {
		return http.StatusOK, assumeRoleXML(t, time.Now().Add(time.Hour))
	})
	handler := configuredHandler(t, url)
	response, err := authenticateInput(handler, map[string]any{"role_arn": roleArn})
	if err != nil {
		t.Fatalf("authenticate error = %v, want nil", err)
	}
	serialized, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshaling response: %v", err)
	}
	total := strings.Count(string(serialized), secretKeyCanary) + strings.Count(string(serialized), sessionTokenCanary)
	inside := strings.Count(response.Credentials["AWS_SECRET_ACCESS_KEY"], secretKeyCanary) + strings.Count(response.Credentials["AWS_SESSION_TOKEN"], sessionTokenCanary)
	if total != inside || inside != 2 {
		t.Fatalf("canaries appear %d times in response (%d inside credentials), want exactly once each inside the typed map: %s", total, inside, serialized)
	}
}
