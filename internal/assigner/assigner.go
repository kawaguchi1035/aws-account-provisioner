// Package assigner grants IAM Identity Center permission sets on a freshly
// created account.
//
// Assignments are applied as soon as an account becomes available rather than
// as a separate pass at the end, so a long-running batch does not leave earlier
// accounts sitting there unreachable.
package assigner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
)

// Defaults for polling. Assignments settle in seconds, not minutes.
const (
	DefaultPollInterval = 2 * time.Second
	DefaultPollTimeout  = 5 * time.Minute
)

// API is the slice of AWS SSO Admin this package uses.
type API interface {
	CreateAccountAssignment(ctx context.Context, params *ssoadmin.CreateAccountAssignmentInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.CreateAccountAssignmentOutput, error)
	DescribeAccountAssignmentCreationStatus(ctx context.Context, params *ssoadmin.DescribeAccountAssignmentCreationStatusInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.DescribeAccountAssignmentCreationStatusOutput, error)
}

// Resolver turns the names used in the input file into Identity Center
// identifiers. discovery.Catalog satisfies it.
type Resolver interface {
	PermissionSetARN(name string) (string, bool)
	PrincipalID(t inputfile.PrincipalType, name string) (string, bool)
}

// Assigner applies assignments to one account at a time.
type Assigner struct {
	API         API
	Resolver    Resolver
	InstanceARN string

	PollInterval time.Duration
	PollTimeout  time.Duration

	// Progress, when set, is called for each assignment that is applied.
	Progress func(accountName, message string)
}

// Assign applies every assignment to accountID.
//
// All of them are attempted even if one fails, and the failures are returned
// together: a half-assigned account is easier to finish by hand when you can
// see everything that is missing at once.
func (a Assigner) Assign(ctx context.Context, account inputfile.Account, accountID string) error {
	var failures []string

	for _, assignment := range account.Assignments {
		if err := a.one(ctx, accountID, assignment); err != nil {
			failures = append(failures, fmt.Sprintf("%s %s / %s: %v",
				strings.ToLower(string(assignment.PrincipalType)),
				assignment.PrincipalName, assignment.PermissionSetName, err))
			continue
		}
		a.report(account.Name, fmt.Sprintf("assigned %s to %s %s",
			assignment.PermissionSetName,
			strings.ToLower(string(assignment.PrincipalType)),
			assignment.PrincipalName))
	}

	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d assignments failed:\n    %s",
		len(failures), len(account.Assignments), strings.Join(failures, "\n    "))
}

func (a Assigner) one(ctx context.Context, accountID string, assignment inputfile.Assignment) error {
	permissionSetARN, ok := a.Resolver.PermissionSetARN(assignment.PermissionSetName)
	if !ok {
		return fmt.Errorf("permission set %q is no longer available", assignment.PermissionSetName)
	}
	principalID, ok := a.Resolver.PrincipalID(assignment.PrincipalType, assignment.PrincipalName)
	if !ok {
		return fmt.Errorf("%s %q is no longer available",
			strings.ToLower(string(assignment.PrincipalType)), assignment.PrincipalName)
	}

	principalType, err := principalType(assignment.PrincipalType)
	if err != nil {
		return err
	}

	out, err := a.API.CreateAccountAssignment(ctx, &ssoadmin.CreateAccountAssignmentInput{
		InstanceArn:      aws.String(a.InstanceARN),
		TargetId:         aws.String(accountID),
		TargetType:       ssotypes.TargetTypeAwsAccount,
		PermissionSetArn: aws.String(permissionSetARN),
		PrincipalId:      aws.String(principalID),
		PrincipalType:    principalType,
	})
	if err != nil {
		return fmt.Errorf("request assignment: %w", err)
	}
	if out.AccountAssignmentCreationStatus == nil {
		return errors.New("no assignment status was returned for the request")
	}

	status := *out.AccountAssignmentCreationStatus
	if settled, err := settle(status); settled {
		return err
	}
	return a.wait(ctx, aws.ToString(status.RequestId))
}

// wait polls until the assignment settles. Unlike account creation, nothing is
// left half-created if this is interrupted: the assignment either exists or it
// does not.
func (a Assigner) wait(ctx context.Context, requestID string) error {
	if requestID == "" {
		return errors.New("no request ID was returned for the assignment")
	}

	interval := a.PollInterval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	timeout := a.PollTimeout
	if timeout <= 0 {
		timeout = DefaultPollTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("stopped waiting after %s: %w", timeout, ctx.Err())
		case <-ticker.C:
		}

		out, err := a.API.DescribeAccountAssignmentCreationStatus(ctx, &ssoadmin.DescribeAccountAssignmentCreationStatusInput{
			InstanceArn:                        aws.String(a.InstanceARN),
			AccountAssignmentCreationRequestId: aws.String(requestID),
		})
		if err != nil {
			return fmt.Errorf("check assignment status: %w", err)
		}
		if out.AccountAssignmentCreationStatus == nil {
			return errors.New("no assignment status was returned")
		}
		if settled, err := settle(*out.AccountAssignmentCreationStatus); settled {
			return err
		}
	}
}

// settle reports whether the status is final, and the error if it failed.
func settle(status ssotypes.AccountAssignmentOperationStatus) (bool, error) {
	switch status.Status {
	case ssotypes.StatusValuesSucceeded:
		return true, nil
	case ssotypes.StatusValuesFailed:
		reason := aws.ToString(status.FailureReason)
		if reason == "" {
			reason = "no reason was reported"
		}
		return true, errors.New(reason)
	default:
		return false, nil
	}
}

func principalType(t inputfile.PrincipalType) (ssotypes.PrincipalType, error) {
	switch t {
	case inputfile.PrincipalGroup:
		return ssotypes.PrincipalTypeGroup, nil
	case inputfile.PrincipalUser:
		return ssotypes.PrincipalTypeUser, nil
	default:
		return "", fmt.Errorf("unknown principal type %q", t)
	}
}

func (a Assigner) report(accountName, message string) {
	if a.Progress != nil {
		a.Progress(accountName, message)
	}
}
