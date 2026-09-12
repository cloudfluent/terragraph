package secretsmanager

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	sdk "github.com/cloudfluent/terragraph/plugin"
)

// acceptedAuthKeys is the closed authenticate reference vocabulary; rejecting unknown keys keeps an HCL typo from silently authenticating a different principal than the one the plan was approved for.
const acceptedAuthKeys = "role_arn, profile, and session_name"

// defaultSessionName keeps repeated acquires of one binding on a comparable identity because the assumed-role ARN embeds the session name.
const defaultSessionName = "terragraph-secretsmanager"

// authRef is one parsed credential binding reference; it names a target principal, it never carries a secret.
type authRef struct {
	roleArn     string
	profile     string
	sessionName string
}

// parseAuthRef is the trust boundary for credential references: values must be strings because HCL could hand over any cty type.
func parseAuthRef(reference map[string]any) (authRef, error) {
	ref := authRef{sessionName: defaultSessionName}
	for key, value := range reference {
		text, ok := value.(string)
		if !ok {
			return authRef{}, fmt.Errorf("plugin.secretsmanager.authenticate: reference key %s must be a string", key)
		}
		switch key {
		case "role_arn":
			ref.roleArn = text
		case "profile":
			ref.profile = text
		case "session_name":
			ref.sessionName = text
		default:
			return authRef{}, fmt.Errorf("plugin.secretsmanager.authenticate: unknown reference key %q; allowed keys are %s", key, acceptedAuthKeys)
		}
	}
	if ref.sessionName == "" {
		return authRef{}, fmt.Errorf("plugin.secretsmanager.authenticate: reference key \"session_name\" must not be empty; remove it to use the default %s session", defaultSessionName)
	}
	return ref, nil
}

// authenticate resolves one credential set from the standard external chain, optionally crossing an STS AssumeRole hop; secrets travel only inside the typed Credentials map and identity is a non-secret ARN the host compares against saved plans.
func (h *handler) authenticate(ctx context.Context, reference map[string]any) (sdk.Response, error) {
	ref, err := parseAuthRef(reference)
	if err != nil {
		return sdk.Response{}, err
	}
	if h.region == "" {
		return sdk.Response{}, fmt.Errorf("plugin.secretsmanager.authenticate: region is not configured; set region in the plugin secretsmanager block before acquiring credentials")
	}
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(h.region)}
	if h.endpoint != "" {
		// Same localstack escape hatch as read so one configure surface pins every STS call this plugin makes.
		options = append(options, awsconfig.WithBaseEndpoint(h.endpoint))
	}
	if ref.profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(ref.profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return awsFault(err), nil
	}
	client := sts.NewFromConfig(cfg)
	if ref.roleArn == "" {
		return staticCredentials(ctx, cfg, client)
	}
	return assumedCredentials(ctx, client, ref)
}

// staticCredentials uses the chain credentials directly; the lease stays nil because static keys do not expire inside one run and the host treats a nil lease as session-scoped.
func staticCredentials(ctx context.Context, cfg aws.Config, client *sts.Client) (sdk.Response, error) {
	value, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return awsFault(err), nil
	}
	caller, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return awsFault(err), nil
	}
	if caller.Arn == nil || *caller.Arn == "" {
		return sdk.Response{}, fmt.Errorf("plugin.secretsmanager.authenticate: STS GetCallerIdentity returned no ARN; the host requires a stable non-secret identity for saved-plan comparison")
	}
	return sdk.Response{Identity: *caller.Arn, Credentials: credentialEnv(value.AccessKeyID, value.SecretAccessKey, value.SessionToken)}, nil
}

// assumedCredentials crosses one AssumeRole hop; the lease carries the STS expiration with a zero RenewAt because rotation cannot reach an already-running subprocess — the host stops the run at expiry with its obtain-a-new-lease remedy instead.
func assumedCredentials(ctx context.Context, client *sts.Client, ref authRef) (sdk.Response, error) {
	output, err := client.AssumeRole(ctx, &sts.AssumeRoleInput{RoleArn: aws.String(ref.roleArn), RoleSessionName: aws.String(ref.sessionName)})
	if err != nil {
		return awsFault(err), nil
	}
	if output.Credentials == nil || output.Credentials.AccessKeyId == nil || output.Credentials.SecretAccessKey == nil || output.AssumedRoleUser == nil || output.AssumedRoleUser.Arn == nil {
		return sdk.Response{}, fmt.Errorf("plugin.secretsmanager.authenticate: STS returned an incomplete AssumeRole result for %s; retry, and if it persists inspect the trust policy on the role", ref.roleArn)
	}
	response := sdk.Response{Identity: *output.AssumedRoleUser.Arn, Credentials: credentialEnv(*output.Credentials.AccessKeyId, *output.Credentials.SecretAccessKey, aws.ToString(output.Credentials.SessionToken))}
	if output.Credentials.Expiration != nil && output.Credentials.Expiration.After(time.Now()) {
		response.Lease = &sdk.Lease{ID: *output.AssumedRoleUser.Arn, ExpiresAt: *output.Credentials.Expiration}
	}
	return response, nil
}

// credentialEnv returns exactly the three names the AWS SDK and terraform both read, dropping an absent session token so a static pair does not shadow a token from another source.
func credentialEnv(accessKeyID, secretAccessKey, sessionToken string) map[string]string {
	env := map[string]string{"AWS_ACCESS_KEY_ID": accessKeyID, "AWS_SECRET_ACCESS_KEY": secretAccessKey}
	if sessionToken != "" {
		env["AWS_SESSION_TOKEN"] = sessionToken
	}
	return env
}
