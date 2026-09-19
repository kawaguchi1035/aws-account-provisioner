// Package provisioner creates accounts through Control Tower Account Factory.
//
// Account creation takes 20-40 minutes and Control Tower serialises the work on
// its own side, so every request is submitted first and then watched
// concurrently. One slow account never blocks the others from being reported.
package provisioner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/servicecatalog"
	sctypes "github.com/aws/aws-sdk-go-v2/service/servicecatalog/types"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
)

// Defaults for polling. Control Tower takes 20-40 minutes per account, so the
// interval is deliberately coarse and the ceiling generous.
const (
	DefaultPollInterval = 30 * time.Second
	DefaultPollTimeout  = 60 * time.Minute
)

// accountIDOutputKey is the Service Catalog record output holding the new
// account's ID.
const accountIDOutputKey = "AccountId"

// API is the slice of Service Catalog this package uses.
type API interface {
	ProvisionProduct(ctx context.Context, params *servicecatalog.ProvisionProductInput, optFns ...func(*servicecatalog.Options)) (*servicecatalog.ProvisionProductOutput, error)
	DescribeRecord(ctx context.Context, params *servicecatalog.DescribeRecordInput, optFns ...func(*servicecatalog.Options)) (*servicecatalog.DescribeRecordOutput, error)
}

// Provisioner submits and watches account creations.
type Provisioner struct {
	API        API
	ProductID  string
	ArtifactID string

	// SSOUserFirstName and SSOUserLastName name the initial Identity Center
	// user Account Factory creates for each account.
	SSOUserFirstName string
	SSOUserLastName  string

	PollInterval time.Duration
	PollTimeout  time.Duration

	// Progress, when set, is called as each account changes state. It may be
	// called from several goroutines, so implementations must be safe to share.
	Progress func(accountName, message string)
}

// Result is the outcome for one account.
type Result struct {
	Account   inputfile.Account
	AccountID string
	Err       error
}

// Succeeded reports whether the account was created.
func (r Result) Succeeded() bool { return r.Err == nil && r.AccountID != "" }

// ErrSubmitted is wrapped into the error of any account whose creation was
// requested but not observed to completion. AWS keeps working on those, so the
// operator must check the console rather than assume nothing happened.
var ErrSubmitted = errors.New("creation was already requested and continues on the AWS side")

// Provision creates every account and waits for all of them.
//
// Results come back in input order. An account that fails does not stop the
// others: every outcome is reported together at the end.
func (p Provisioner) Provision(ctx context.Context, accounts []inputfile.Account) []Result {
	results := make([]Result, len(accounts))
	recordIDs := make([]string, len(accounts))

	// Submit everything first. Control Tower queues the work, so there is
	// nothing to gain from submitting one account at a time.
	for i, account := range accounts {
		results[i].Account = account

		recordID, err := p.submit(ctx, account)
		if err != nil {
			results[i].Err = err
			p.report(account.Name, fmt.Sprintf("request failed: %v", err))
			continue
		}
		recordIDs[i] = recordID
		p.report(account.Name, "creation requested")
	}

	var wg sync.WaitGroup
	for i := range accounts {
		if recordIDs[i] == "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			accountID, err := p.wait(ctx, recordIDs[i], accounts[i].Name)
			results[i].AccountID = accountID
			results[i].Err = err
		}(i)
	}
	wg.Wait()

	return results
}

func (p Provisioner) submit(ctx context.Context, account inputfile.Account) (string, error) {
	out, err := p.API.ProvisionProduct(ctx, &servicecatalog.ProvisionProductInput{
		ProductId:              aws.String(p.ProductID),
		ProvisioningArtifactId: aws.String(p.ArtifactID),
		ProvisionedProductName: aws.String("account-" + account.Name),
		ProvisioningParameters: p.parameters(account),
	})
	if err != nil {
		return "", fmt.Errorf("request account creation: %w", err)
	}
	if out.RecordDetail == nil || out.RecordDetail.RecordId == nil {
		return "", errors.New("no record ID was returned for the request")
	}
	return aws.ToString(out.RecordDetail.RecordId), nil
}

func (p Provisioner) parameters(account inputfile.Account) []sctypes.ProvisioningParameter {
	// Control Tower identifies a target OU by "Name (ou-id)", which is exactly
	// the form the input file uses.
	ou := fmt.Sprintf("%s (%s)", account.OUName, account.OUID)

	return []sctypes.ProvisioningParameter{
		{Key: aws.String("AccountEmail"), Value: aws.String(account.Email)},
		{Key: aws.String("AccountName"), Value: aws.String(account.Name)},
		{Key: aws.String("ManagedOrganizationalUnit"), Value: aws.String(ou)},
		{Key: aws.String("SSOUserEmail"), Value: aws.String(account.Email)},
		{Key: aws.String("SSOUserFirstName"), Value: aws.String(p.SSOUserFirstName)},
		{Key: aws.String("SSOUserLastName"), Value: aws.String(p.SSOUserLastName)},
	}
}

// wait polls until the record settles, the deadline passes, or ctx is done.
func (p Provisioner) wait(ctx context.Context, recordID, accountName string) (string, error) {
	interval := p.PollInterval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	timeout := p.PollTimeout
	if timeout <= 0 {
		timeout = DefaultPollTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastStatus := sctypes.RecordStatus("")
	for {
		out, err := p.API.DescribeRecord(ctx, &servicecatalog.DescribeRecordInput{Id: aws.String(recordID)})
		if err != nil {
			return "", fmt.Errorf("check creation status: %w (%w)", err, ErrSubmitted)
		}
		if out.RecordDetail == nil {
			return "", fmt.Errorf("no record detail was returned (%w)", ErrSubmitted)
		}

		if status := out.RecordDetail.Status; status != lastStatus {
			lastStatus = status
			p.report(accountName, string(status))
		}

		switch out.RecordDetail.Status {
		case sctypes.RecordStatusSucceeded:
			return accountID(out.RecordOutputs)
		case sctypes.RecordStatusFailed:
			return "", fmt.Errorf("account creation failed: %s", recordError(out.RecordDetail.RecordErrors))
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("stopped waiting after %s: %w (%w)", timeout, ctx.Err(), ErrSubmitted)
		case <-ticker.C:
		}
	}
}

func accountID(outputs []sctypes.RecordOutput) (string, error) {
	for _, o := range outputs {
		if aws.ToString(o.OutputKey) != accountIDOutputKey {
			continue
		}
		id := aws.ToString(o.OutputValue)
		if id == "" {
			return "", errors.New("the creation record reported an empty account ID")
		}
		return id, nil
	}
	return "", errors.New("the creation record contained no account ID")
}

func recordError(errs []sctypes.RecordError) string {
	if len(errs) == 0 {
		return "no reason was reported"
	}
	if d := aws.ToString(errs[0].Description); d != "" {
		return d
	}
	return aws.ToString(errs[0].Code)
}

func (p Provisioner) report(accountName, message string) {
	if p.Progress != nil {
		p.Progress(accountName, message)
	}
}
