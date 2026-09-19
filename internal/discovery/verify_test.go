package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
)

func loadForTest(t *testing.T) *Catalog {
	t.Helper()
	catalog, err := Load(context.Background(), workingDeps())
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	return catalog
}

func account(mutate ...func(*inputfile.Account)) inputfile.Account {
	a := inputfile.Account{
		Email:  "aws+dev@example.com",
		Name:   "dev-account",
		OUName: "Sandbox",
		OUID:   "ou-abcd-12345678",
		Line:   2,
		Assignments: []inputfile.Assignment{{
			PrincipalType:     inputfile.PrincipalGroup,
			PrincipalName:     "Developers",
			PermissionSetName: "AdministratorAccess",
			Line:              2,
		}},
	}
	for _, m := range mutate {
		m(&a)
	}
	return a
}

func TestVerifyAcceptsAValidPlan(t *testing.T) {
	catalog := loadForTest(t)
	if err := catalog.Verify([]inputfile.Account{account()}); err != nil {
		t.Errorf("Verify() returned unexpected error: %v", err)
	}
}

func TestVerifyRejectsMissingResources(t *testing.T) {
	catalog := loadForTest(t)

	tests := []struct {
		name   string
		mutate func(*inputfile.Account)
		want   string
	}{
		{
			name:   "unknown OU",
			mutate: func(a *inputfile.Account) { a.OUID = "ou-abcd-99999999" },
			want:   "OU ou-abcd-99999999 does not exist",
		},
		{
			name:   "OU name disagrees with AWS",
			mutate: func(a *inputfile.Account) { a.OUName = "Stale" },
			want:   `is named "Sandbox" in AWS, but the file says "Stale"`,
		},
		{
			name:   "unknown permission set",
			mutate: func(a *inputfile.Account) { a.Assignments[0].PermissionSetName = "NoSuchAccess" },
			want:   `permission set "NoSuchAccess" does not exist`,
		},
		{
			name:   "unknown group",
			mutate: func(a *inputfile.Account) { a.Assignments[0].PrincipalName = "NoSuchGroup" },
			want:   `group "NoSuchGroup" does not exist in IAM Identity Center`,
		},
		{
			name: "user looked up against the group list",
			mutate: func(a *inputfile.Account) {
				a.Assignments[0].PrincipalType = inputfile.PrincipalUser
				a.Assignments[0].PrincipalName = "Developers"
			},
			want: `user "Developers" does not exist`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := catalog.Verify([]inputfile.Account{account(tt.mutate)})
			if err == nil {
				t.Fatal("Verify() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "line 2") {
				t.Errorf("error %q does not point at a line", err)
			}
		})
	}
}

func TestVerifySuggestsCloseNames(t *testing.T) {
	catalog := loadForTest(t)

	tests := []struct {
		name      string
		mutate    func(*inputfile.Account)
		want      string
		wantNoHit string
	}{
		{
			name:   "permission set typo",
			mutate: func(a *inputfile.Account) { a.Assignments[0].PermissionSetName = "AdministratorAcess" },
			want:   `did you mean "AdministratorAccess"?`,
		},
		{
			name:   "group typo",
			mutate: func(a *inputfile.Account) { a.Assignments[0].PrincipalName = "Developer" },
			want:   `did you mean "Developers"?`,
		},
		{
			name:      "unrelated name gets no suggestion",
			mutate:    func(a *inputfile.Account) { a.Assignments[0].PrincipalName = "Marketing" },
			wantNoHit: "did you mean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := catalog.Verify([]inputfile.Account{account(tt.mutate)})
			if err == nil {
				t.Fatal("Verify() = nil error, want error")
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not suggest a correction", err)
			}
			if tt.wantNoHit != "" && strings.Contains(err.Error(), tt.wantNoHit) {
				t.Errorf("error %q offers a suggestion for an unrelated name", err)
			}
		})
	}
}

func TestVerifyReportsEveryProblemAtOnce(t *testing.T) {
	catalog := loadForTest(t)

	bad := account(func(a *inputfile.Account) {
		a.OUID = "ou-abcd-99999999"
		a.Assignments[0].PermissionSetName = "NoSuchAccess"
		a.Assignments[0].PrincipalName = "NoSuchGroup"
	})

	err := catalog.Verify([]inputfile.Account{bad})
	if err == nil {
		t.Fatal("Verify() = nil error, want error")
	}
	if !strings.HasPrefix(err.Error(), "3 problems found in AWS:") {
		t.Errorf("summary = %q, want all three problems reported together", err)
	}
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{a: "", b: "", want: 0},
		{a: "abc", b: "abc", want: 0},
		{a: "abc", b: "abd", want: 1},
		{a: "abc", b: "ab", want: 1},
		{a: "", b: "abc", want: 3},
		{a: "kitten", b: "sitting", want: 3},
	}

	for _, tt := range tests {
		if got := editDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
