// Package awsauth resolves the AWS configuration the tool runs with, optionally
// assuming a role, and verifies that it really reached the expected account.
package awsauth

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// SessionName identifies this tool in CloudTrail when a role is assumed.
const SessionName = "aws-account-provisioner"

// Options describes how to reach the Organizations management account.
type Options struct {
	// Profile is the shared-config profile to start from. Empty means the
	// default credential chain.
	Profile string

	// RootAccountID is the management account the tool must end up in.
	RootAccountID string

	// AssumeRoleName, when set, is assumed in RootAccountID. When empty, the
	// profile's own credentials are used as-is.
	AssumeRoleName string
}

// callerIdentityAPI is the slice of STS this package needs, so tests can stub it.
type callerIdentityAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Resolve builds an aws.Config, applying the AssumeRole step when configured,
// and confirms the resulting credentials belong to Options.RootAccountID.
//
// The region comes from the standard resolution chain (profile, AWS_REGION).
// It must be the Control Tower home region, since that is where Account Factory
// lives.
func Resolve(ctx context.Context, opts Options) (aws.Config, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{}
	if opts.Profile != "" {
		loadOpts = append(loadOpts, awsconfig.WithSharedConfigProfile(opts.Profile))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load AWS configuration: %w", err)
	}

	if opts.AssumeRoleName != "" {
		roleARN := RoleARN(opts.RootAccountID, opts.AssumeRoleName)
		provider := stscreds.NewAssumeRoleProvider(sts.NewFromConfig(cfg), roleARN, func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = SessionName
		})
		cfg.Credentials = aws.NewCredentialsCache(provider)
	}

	if err := verify(ctx, sts.NewFromConfig(cfg), opts.RootAccountID); err != nil {
		return aws.Config{}, err
	}
	return cfg, nil
}

// RoleARN builds the ARN of the role to assume in the management account.
func RoleARN(accountID, roleName string) string {
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)
}

// verify guards against a mistyped or stale profile pointing somewhere else.
// Creating an AWS account cannot be undone, so this check runs before anything
// else happens.
func verify(ctx context.Context, api callerIdentityAPI, wantAccountID string) error {
	out, err := api.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("verify caller identity: %w", err)
	}
	return checkAccount(aws.ToString(out.Account), wantAccountID)
}

func checkAccount(got, want string) error {
	if got == "" {
		return fmt.Errorf("STS returned no account ID")
	}
	if got != want {
		return fmt.Errorf("credentials resolve to account %s, but ROOT_ACCOUNT_ID is %s", got, want)
	}
	return nil
}
