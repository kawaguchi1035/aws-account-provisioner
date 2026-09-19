package provisioner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/servicecatalog"
	sctypes "github.com/aws/aws-sdk-go-v2/service/servicecatalog/types"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
)

// scriptedAPI answers ProvisionProduct and DescribeRecord from per-account
// scripts, keyed by the account name embedded in the provisioned product name.
type scriptedAPI struct {
	mu sync.Mutex

	// submitErr maps an account name to an error ProvisionProduct should return.
	submitErr map[string]error
	// noRecordID lists account names for which ProvisionProduct returns nothing.
	noRecordID map[string]bool

	// statuses maps a record ID to the sequence of statuses DescribeRecord
	// returns, one per call; the last entry repeats.
	statuses map[string][]sctypes.RecordStatus
	// accountIDs maps a record ID to the account ID reported on success.
	accountIDs map[string]string
	// describeErr maps a record ID to an error DescribeRecord should return.
	describeErr map[string]error
	// failureReason maps a record ID to the description reported on failure.
	failureReason map[string]string

	calls   map[string]int
	params  map[string][]sctypes.ProvisioningParameter
	records map[string]string // record ID -> account name
}

func newScriptedAPI() *scriptedAPI {
	return &scriptedAPI{
		submitErr:     map[string]error{},
		noRecordID:    map[string]bool{},
		statuses:      map[string][]sctypes.RecordStatus{},
		accountIDs:    map[string]string{},
		describeErr:   map[string]error{},
		failureReason: map[string]string{},
		calls:         map[string]int{},
		params:        map[string][]sctypes.ProvisioningParameter{},
		records:       map[string]string{},
	}
}

func (a *scriptedAPI) ProvisionProduct(_ context.Context, in *servicecatalog.ProvisionProductInput, _ ...func(*servicecatalog.Options)) (*servicecatalog.ProvisionProductOutput, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	name := strings.TrimPrefix(aws.ToString(in.ProvisionedProductName), "account-")
	a.params[name] = in.ProvisioningParameters

	if err := a.submitErr[name]; err != nil {
		return nil, err
	}
	if a.noRecordID[name] {
		return &servicecatalog.ProvisionProductOutput{}, nil
	}

	recordID := "rec-" + name
	a.records[recordID] = name
	return &servicecatalog.ProvisionProductOutput{
		RecordDetail: &sctypes.RecordDetail{RecordId: aws.String(recordID)},
	}, nil
}

func (a *scriptedAPI) DescribeRecord(_ context.Context, in *servicecatalog.DescribeRecordInput, _ ...func(*servicecatalog.Options)) (*servicecatalog.DescribeRecordOutput, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	id := aws.ToString(in.Id)
	if err := a.describeErr[id]; err != nil {
		return nil, err
	}

	n := a.calls[id]
	a.calls[id]++

	statuses := a.statuses[id]
	if len(statuses) == 0 {
		statuses = []sctypes.RecordStatus{sctypes.RecordStatusSucceeded}
	}
	status := statuses[min(n, len(statuses)-1)]

	out := &servicecatalog.DescribeRecordOutput{
		RecordDetail: &sctypes.RecordDetail{Status: status},
	}
	switch status {
	case sctypes.RecordStatusSucceeded:
		if accountID, ok := a.accountIDs[id]; ok {
			out.RecordOutputs = []sctypes.RecordOutput{{
				OutputKey:   aws.String(accountIDOutputKey),
				OutputValue: aws.String(accountID),
			}}
		}
	case sctypes.RecordStatusFailed:
		out.RecordDetail.RecordErrors = []sctypes.RecordError{{
			Description: aws.String(a.failureReason[id]),
		}}
	}
	return out, nil
}

func testAccount(name string) inputfile.Account {
	return inputfile.Account{
		Email:  "aws+" + name + "@example.com",
		Name:   name,
		OUName: "Sandbox",
		OUID:   "ou-abcd-12345678",
	}
}

func newProvisioner(api API) Provisioner {
	return Provisioner{
		API:              api,
		ProductID:        "prod-af",
		ArtifactID:       "pa-1",
		SSOUserFirstName: "Admin",
		SSOUserLastName:  "User",
		PollInterval:     time.Millisecond,
		PollTimeout:      2 * time.Second,
	}
}

func TestProvisionCreatesEveryAccount(t *testing.T) {
	api := newScriptedAPI()
	api.accountIDs["rec-dev"] = "111111111111"
	api.accountIDs["rec-stg"] = "222222222222"
	api.statuses["rec-dev"] = []sctypes.RecordStatus{
		sctypes.RecordStatusCreated,
		sctypes.RecordStatusInProgress,
		sctypes.RecordStatusSucceeded,
	}

	results := newProvisioner(api).Provision(context.Background(),
		[]inputfile.Account{testAccount("dev"), testAccount("stg")})

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	// Results keep input order regardless of which account finished first.
	if results[0].Account.Name != "dev" || results[1].Account.Name != "stg" {
		t.Errorf("results are out of order: %q, %q", results[0].Account.Name, results[1].Account.Name)
	}
	for _, r := range results {
		if !r.Succeeded() {
			t.Errorf("%s did not succeed: %v", r.Account.Name, r.Err)
		}
	}
	if results[0].AccountID != "111111111111" {
		t.Errorf("AccountID = %q, want the value from the record output", results[0].AccountID)
	}
}

func TestProvisionSendsTheExpectedParameters(t *testing.T) {
	api := newScriptedAPI()
	api.accountIDs["rec-dev"] = "111111111111"

	newProvisioner(api).Provision(context.Background(), []inputfile.Account{testAccount("dev")})

	got := map[string]string{}
	for _, p := range api.params["dev"] {
		got[aws.ToString(p.Key)] = aws.ToString(p.Value)
	}

	want := map[string]string{
		"AccountEmail":              "aws+dev@example.com",
		"AccountName":               "dev",
		"ManagedOrganizationalUnit": "Sandbox (ou-abcd-12345678)",
		"SSOUserEmail":              "aws+dev@example.com",
		"SSOUserFirstName":          "Admin",
		"SSOUserLastName":           "User",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parameter %s = %q, want %q", k, got[k], v)
		}
	}
}

func TestProvisionContinuesAfterOneAccountFails(t *testing.T) {
	api := newScriptedAPI()
	api.statuses["rec-dev"] = []sctypes.RecordStatus{sctypes.RecordStatusFailed}
	api.failureReason["rec-dev"] = "email already in use"
	api.accountIDs["rec-stg"] = "222222222222"

	results := newProvisioner(api).Provision(context.Background(),
		[]inputfile.Account{testAccount("dev"), testAccount("stg")})

	if results[0].Succeeded() {
		t.Error("the failing account was reported as successful")
	}
	if !strings.Contains(results[0].Err.Error(), "email already in use") {
		t.Errorf("error %q does not carry the reason AWS gave", results[0].Err)
	}
	if !results[1].Succeeded() {
		t.Errorf("the second account was not created: %v", results[1].Err)
	}
}

func TestProvisionReportsSubmitFailures(t *testing.T) {
	api := newScriptedAPI()
	api.submitErr["dev"] = errors.New("access denied")

	results := newProvisioner(api).Provision(context.Background(), []inputfile.Account{testAccount("dev")})

	if results[0].Err == nil {
		t.Fatal("a failed request was reported as successful")
	}
	if errors.Is(results[0].Err, ErrSubmitted) {
		t.Error("a request that never reached AWS was marked as still in flight")
	}
}

func TestProvisionFlagsInFlightWorkOnTimeout(t *testing.T) {
	api := newScriptedAPI()
	api.statuses["rec-dev"] = []sctypes.RecordStatus{sctypes.RecordStatusInProgress}

	p := newProvisioner(api)
	p.PollTimeout = 20 * time.Millisecond
	results := p.Provision(context.Background(), []inputfile.Account{testAccount("dev")})

	if results[0].Err == nil {
		t.Fatal("the timeout was not reported")
	}
	if !errors.Is(results[0].Err, ErrSubmitted) {
		t.Errorf("error %q does not warn that AWS is still working on it", results[0].Err)
	}
}

func TestProvisionFlagsInFlightWorkOnCancellation(t *testing.T) {
	api := newScriptedAPI()
	api.statuses["rec-dev"] = []sctypes.RecordStatus{sctypes.RecordStatusInProgress}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	results := newProvisioner(api).Provision(ctx, []inputfile.Account{testAccount("dev")})

	if results[0].Err == nil {
		t.Fatal("cancellation was not reported")
	}
	if !errors.Is(results[0].Err, ErrSubmitted) {
		t.Errorf("error %q does not warn that AWS is still working on it", results[0].Err)
	}
}

func TestProvisionRejectsRecordsWithoutAnAccountID(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*scriptedAPI)
		want  string
	}{
		{
			name:  "no account ID in the outputs",
			setup: func(*scriptedAPI) {},
			want:  "no account ID",
		},
		{
			name:  "no record ID from the request",
			setup: func(a *scriptedAPI) { a.noRecordID["dev"] = true },
			want:  "no record ID",
		},
		{
			name:  "describe fails",
			setup: func(a *scriptedAPI) { a.describeErr["rec-dev"] = errors.New("throttled") },
			want:  "check creation status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newScriptedAPI()
			tt.setup(api)

			results := newProvisioner(api).Provision(context.Background(), []inputfile.Account{testAccount("dev")})
			if results[0].Err == nil {
				t.Fatal("the problem was not reported")
			}
			if !strings.Contains(results[0].Err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", results[0].Err, tt.want)
			}
		})
	}
}

func TestProvisionReportsProgressOncePerStatusChange(t *testing.T) {
	api := newScriptedAPI()
	api.accountIDs["rec-dev"] = "111111111111"
	api.statuses["rec-dev"] = []sctypes.RecordStatus{
		sctypes.RecordStatusInProgress,
		sctypes.RecordStatusInProgress,
		sctypes.RecordStatusInProgress,
		sctypes.RecordStatusSucceeded,
	}

	var mu sync.Mutex
	var messages []string

	p := newProvisioner(api)
	p.Progress = func(_, message string) {
		mu.Lock()
		defer mu.Unlock()
		messages = append(messages, message)
	}
	p.Provision(context.Background(), []inputfile.Account{testAccount("dev")})

	// "creation requested", then IN_PROGRESS once despite three polls, then SUCCEEDED.
	want := []string{"creation requested", "IN_PROGRESS", "SUCCEEDED"}
	if len(messages) != len(want) {
		t.Fatalf("got %v, want %v", messages, want)
	}
	for i := range want {
		if messages[i] != want[i] {
			t.Errorf("message %d = %q, want %q", i, messages[i], want[i])
		}
	}
}

func TestProvisionRunsOnCreatedForEachAccount(t *testing.T) {
	api := newScriptedAPI()
	api.accountIDs["rec-dev"] = "111111111111"
	api.accountIDs["rec-stg"] = "222222222222"

	var mu sync.Mutex
	seen := map[string]string{}

	p := newProvisioner(api)
	p.OnCreated = func(_ context.Context, account inputfile.Account, accountID string) error {
		mu.Lock()
		defer mu.Unlock()
		seen[account.Name] = accountID
		if account.Name == "stg" {
			return errors.New("assignment failed")
		}
		return nil
	}
	results := p.Provision(context.Background(), []inputfile.Account{testAccount("dev"), testAccount("stg")})

	if seen["dev"] != "111111111111" || seen["stg"] != "222222222222" {
		t.Errorf("OnCreated saw %v, want both accounts with their IDs", seen)
	}
	if results[0].AfterErr != nil {
		t.Errorf("dev reported AfterErr = %v, want nil", results[0].AfterErr)
	}
	// A failing OnCreated must not turn a created account into a failed one.
	if !results[1].Succeeded() {
		t.Error("stg was reported as not created even though creation succeeded")
	}
	if results[1].AfterErr == nil {
		t.Error("the OnCreated failure was not recorded")
	}
}

func TestProvisionSkipsOnCreatedWhenCreationFailed(t *testing.T) {
	api := newScriptedAPI()
	api.statuses["rec-dev"] = []sctypes.RecordStatus{sctypes.RecordStatusFailed}

	called := false
	p := newProvisioner(api)
	p.OnCreated = func(context.Context, inputfile.Account, string) error {
		called = true
		return nil
	}
	p.Provision(context.Background(), []inputfile.Account{testAccount("dev")})

	if called {
		t.Error("OnCreated ran for an account that was never created")
	}
}
