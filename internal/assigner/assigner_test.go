package assigner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
)

type call struct {
	targetID         string
	permissionSetARN string
	principalID      string
	principalType    ssotypes.PrincipalType
}

// scriptedAPI returns the scripted status sequence for each request, keyed by
// the order in which assignments are requested.
type scriptedAPI struct {
	mu sync.Mutex

	calls []call

	// createStatus is the status returned by CreateAccountAssignment. When it
	// is IN_PROGRESS the caller must poll.
	createStatus ssotypes.StatusValues
	createErr    error
	noStatus     bool
	noRequestID  bool

	// pollStatuses is returned by successive DescribeAccountAssignmentCreationStatus
	// calls; the last entry repeats.
	pollStatuses  []ssotypes.StatusValues
	pollErr       error
	failureReason string
	pollCalls     int
}

func (a *scriptedAPI) CreateAccountAssignment(_ context.Context, in *ssoadmin.CreateAccountAssignmentInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.CreateAccountAssignmentOutput, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.calls = append(a.calls, call{
		targetID:         aws.ToString(in.TargetId),
		permissionSetARN: aws.ToString(in.PermissionSetArn),
		principalID:      aws.ToString(in.PrincipalId),
		principalType:    in.PrincipalType,
	})

	if a.createErr != nil {
		return nil, a.createErr
	}
	if a.noStatus {
		return &ssoadmin.CreateAccountAssignmentOutput{}, nil
	}

	status := ssotypes.AccountAssignmentOperationStatus{Status: a.createStatus}
	if !a.noRequestID {
		status.RequestId = aws.String("req-1")
	}
	if a.createStatus == ssotypes.StatusValuesFailed {
		status.FailureReason = aws.String(a.failureReason)
	}
	return &ssoadmin.CreateAccountAssignmentOutput{AccountAssignmentCreationStatus: &status}, nil
}

func (a *scriptedAPI) DescribeAccountAssignmentCreationStatus(context.Context, *ssoadmin.DescribeAccountAssignmentCreationStatusInput, ...func(*ssoadmin.Options)) (*ssoadmin.DescribeAccountAssignmentCreationStatusOutput, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.pollErr != nil {
		return nil, a.pollErr
	}

	n := a.pollCalls
	a.pollCalls++

	statuses := a.pollStatuses
	if len(statuses) == 0 {
		statuses = []ssotypes.StatusValues{ssotypes.StatusValuesSucceeded}
	}
	status := ssotypes.AccountAssignmentOperationStatus{Status: statuses[min(n, len(statuses)-1)]}
	if status.Status == ssotypes.StatusValuesFailed {
		status.FailureReason = aws.String(a.failureReason)
	}
	return &ssoadmin.DescribeAccountAssignmentCreationStatusOutput{AccountAssignmentCreationStatus: &status}, nil
}

type fakeResolver struct {
	permissionSets map[string]string
	groups         map[string]string
	users          map[string]string
}

func (r fakeResolver) PermissionSetARN(name string) (string, bool) {
	arn, ok := r.permissionSets[name]
	return arn, ok
}

func (r fakeResolver) PrincipalID(t inputfile.PrincipalType, name string) (string, bool) {
	if t == inputfile.PrincipalGroup {
		id, ok := r.groups[name]
		return id, ok
	}
	id, ok := r.users[name]
	return id, ok
}

func resolver() fakeResolver {
	return fakeResolver{
		permissionSets: map[string]string{"AdministratorAccess": "arn:ps/admin", "ReadOnlyAccess": "arn:ps/readonly"},
		groups:         map[string]string{"Developers": "g-1"},
		users:          map[string]string{"taro": "u-1"},
	}
}

func account(assignments ...inputfile.Assignment) inputfile.Account {
	return inputfile.Account{
		Email:       "aws+dev@example.com",
		Name:        "dev",
		OUName:      "Sandbox",
		OUID:        "ou-abcd-12345678",
		Assignments: assignments,
	}
}

func groupAssignment() inputfile.Assignment {
	return inputfile.Assignment{
		PrincipalType:     inputfile.PrincipalGroup,
		PrincipalName:     "Developers",
		PermissionSetName: "AdministratorAccess",
	}
}

func userAssignment() inputfile.Assignment {
	return inputfile.Assignment{
		PrincipalType:     inputfile.PrincipalUser,
		PrincipalName:     "taro",
		PermissionSetName: "ReadOnlyAccess",
	}
}

func newAssigner(api API) Assigner {
	return Assigner{
		API:          api,
		Resolver:     resolver(),
		InstanceARN:  "arn:aws:sso:::instance/ssoins-123",
		PollInterval: time.Millisecond,
		PollTimeout:  2 * time.Second,
	}
}

func TestAssignAppliesEveryAssignment(t *testing.T) {
	api := &scriptedAPI{createStatus: ssotypes.StatusValuesSucceeded}

	err := newAssigner(api).Assign(context.Background(), account(groupAssignment(), userAssignment()), "111111111111")
	if err != nil {
		t.Fatalf("Assign() returned unexpected error: %v", err)
	}
	if len(api.calls) != 2 {
		t.Fatalf("got %d assignment requests, want 2", len(api.calls))
	}

	got := api.calls[0]
	if got.targetID != "111111111111" {
		t.Errorf("TargetId = %q", got.targetID)
	}
	if got.permissionSetARN != "arn:ps/admin" {
		t.Errorf("PermissionSetArn = %q, want the resolved ARN", got.permissionSetARN)
	}
	if got.principalID != "g-1" {
		t.Errorf("PrincipalId = %q, want the resolved group ID", got.principalID)
	}
	if got.principalType != ssotypes.PrincipalTypeGroup {
		t.Errorf("PrincipalType = %q, want GROUP", got.principalType)
	}
	if api.calls[1].principalType != ssotypes.PrincipalTypeUser {
		t.Errorf("second PrincipalType = %q, want USER", api.calls[1].principalType)
	}
}

func TestAssignSkipsPollingWhenTheRequestSettlesImmediately(t *testing.T) {
	api := &scriptedAPI{createStatus: ssotypes.StatusValuesSucceeded}

	if err := newAssigner(api).Assign(context.Background(), account(groupAssignment()), "111111111111"); err != nil {
		t.Fatalf("Assign() returned unexpected error: %v", err)
	}
	if api.pollCalls != 0 {
		t.Errorf("polled %d times, want none when the request already succeeded", api.pollCalls)
	}
}

func TestAssignPollsUntilTheRequestSettles(t *testing.T) {
	api := &scriptedAPI{
		createStatus: ssotypes.StatusValuesInProgress,
		pollStatuses: []ssotypes.StatusValues{
			ssotypes.StatusValuesInProgress,
			ssotypes.StatusValuesInProgress,
			ssotypes.StatusValuesSucceeded,
		},
	}

	if err := newAssigner(api).Assign(context.Background(), account(groupAssignment()), "111111111111"); err != nil {
		t.Fatalf("Assign() returned unexpected error: %v", err)
	}
	if api.pollCalls != 3 {
		t.Errorf("polled %d times, want 3", api.pollCalls)
	}
}

func TestAssignAttemptsEveryAssignmentEvenAfterAFailure(t *testing.T) {
	api := &scriptedAPI{
		createStatus:  ssotypes.StatusValuesFailed,
		failureReason: "principal not found",
	}

	err := newAssigner(api).Assign(context.Background(), account(groupAssignment(), userAssignment()), "111111111111")
	if err == nil {
		t.Fatal("Assign() = nil error, want error")
	}
	if len(api.calls) != 2 {
		t.Errorf("made %d requests, want both to be attempted", len(api.calls))
	}
	if !strings.Contains(err.Error(), "2 of 2 assignments failed") {
		t.Errorf("error %q does not summarise the failures", err)
	}
	if !strings.Contains(err.Error(), "principal not found") {
		t.Errorf("error %q does not carry the reason AWS gave", err)
	}
}

func TestAssignReportsUnresolvableNames(t *testing.T) {
	tests := []struct {
		name       string
		assignment inputfile.Assignment
		want       string
	}{
		{
			name: "unknown permission set",
			assignment: inputfile.Assignment{
				PrincipalType: inputfile.PrincipalGroup, PrincipalName: "Developers", PermissionSetName: "Gone",
			},
			want: `permission set "Gone" is no longer available`,
		},
		{
			name: "unknown group",
			assignment: inputfile.Assignment{
				PrincipalType: inputfile.PrincipalGroup, PrincipalName: "Gone", PermissionSetName: "AdministratorAccess",
			},
			want: `group "Gone" is no longer available`,
		},
		{
			name: "unknown principal type",
			assignment: inputfile.Assignment{
				PrincipalType: "ROLE", PrincipalName: "Developers", PermissionSetName: "AdministratorAccess",
			},
			want: "no longer available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &scriptedAPI{createStatus: ssotypes.StatusValuesSucceeded}

			err := newAssigner(api).Assign(context.Background(), account(tt.assignment), "111111111111")
			if err == nil {
				t.Fatal("Assign() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
			if len(api.calls) != 0 {
				t.Error("an unresolvable assignment was still sent to AWS")
			}
		})
	}
}

func TestAssignReportsAPIFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*scriptedAPI)
		want  string
	}{
		{
			name:  "create fails",
			setup: func(a *scriptedAPI) { a.createErr = errors.New("access denied") },
			want:  "request assignment",
		},
		{
			name:  "no status returned",
			setup: func(a *scriptedAPI) { a.noStatus = true },
			want:  "no assignment status",
		},
		{
			name: "no request ID to poll with",
			setup: func(a *scriptedAPI) {
				a.createStatus = ssotypes.StatusValuesInProgress
				a.noRequestID = true
			},
			want: "no request ID",
		},
		{
			name: "polling fails",
			setup: func(a *scriptedAPI) {
				a.createStatus = ssotypes.StatusValuesInProgress
				a.pollErr = errors.New("throttled")
			},
			want: "check assignment status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &scriptedAPI{createStatus: ssotypes.StatusValuesSucceeded}
			tt.setup(api)

			err := newAssigner(api).Assign(context.Background(), account(groupAssignment()), "111111111111")
			if err == nil {
				t.Fatal("Assign() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestAssignTimesOut(t *testing.T) {
	api := &scriptedAPI{
		createStatus: ssotypes.StatusValuesInProgress,
		pollStatuses: []ssotypes.StatusValues{ssotypes.StatusValuesInProgress},
	}

	a := newAssigner(api)
	a.PollTimeout = 20 * time.Millisecond

	err := a.Assign(context.Background(), account(groupAssignment()), "111111111111")
	if err == nil {
		t.Fatal("Assign() = nil error, want a timeout")
	}
	if !strings.Contains(err.Error(), "stopped waiting") {
		t.Errorf("error %q does not describe the timeout", err)
	}
}

func TestAssignReportsProgress(t *testing.T) {
	api := &scriptedAPI{createStatus: ssotypes.StatusValuesSucceeded}

	var messages []string
	a := newAssigner(api)
	a.Progress = func(_, message string) { messages = append(messages, message) }

	if err := a.Assign(context.Background(), account(groupAssignment()), "111111111111"); err != nil {
		t.Fatalf("Assign() returned unexpected error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(messages))
	}
	if !strings.Contains(messages[0], "AdministratorAccess") || !strings.Contains(messages[0], "Developers") {
		t.Errorf("message %q does not say what was assigned to whom", messages[0])
	}
}
