package inputfile

import (
	"errors"
	"strings"
	"testing"
)

const header = "AccountEmail\tAccountName\tOU\tPrincipalType\tPrincipalName\tPermissionSetName\n"

func tsv(rows ...string) string {
	return header + strings.Join(rows, "\n") + "\n"
}

func TestParseGroupsRowsByAccount(t *testing.T) {
	in := tsv(
		"aws+dev@example.com\tdev-account\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
		"aws+dev@example.com\tdev-account\tSandbox (ou-abcd-12345678)\tUSER\ttaro\tReadOnlyAccess",
		"aws+stg@example.com\tstg-account\tStaging (ou-abcd-87654321)\tGROUP\tDevelopers\tAdministratorAccess",
	)

	accounts, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse() returned unexpected error: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}

	dev := accounts[0]
	if dev.Email != "aws+dev@example.com" || dev.Name != "dev-account" {
		t.Errorf("first account = %+v, want the dev account", dev)
	}
	if dev.OUName != "Sandbox" || dev.OUID != "ou-abcd-12345678" {
		t.Errorf("OU = %q / %q, want Sandbox / ou-abcd-12345678", dev.OUName, dev.OUID)
	}
	if len(dev.Assignments) != 2 {
		t.Fatalf("dev account has %d assignments, want 2", len(dev.Assignments))
	}
	if dev.Assignments[0].PrincipalType != PrincipalGroup {
		t.Errorf("first assignment type = %q, want GROUP", dev.Assignments[0].PrincipalType)
	}
	if dev.Assignments[1].Line != 3 {
		t.Errorf("second assignment line = %d, want 3", dev.Assignments[1].Line)
	}

	// Accounts keep the order in which they first appear.
	if accounts[1].Email != "aws+stg@example.com" {
		t.Errorf("second account = %q, want the stg account", accounts[1].Email)
	}
}

func TestParseAcceptsSurroundingWhitespaceAndLowercaseType(t *testing.T) {
	in := tsv("  aws+dev@example.com \t dev-account\tSandbox (ou-abcd-12345678)\tgroup\tDevelopers\tAdministratorAccess")

	accounts, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse() returned unexpected error: %v", err)
	}
	if accounts[0].Email != "aws+dev@example.com" {
		t.Errorf("Email = %q, want the trimmed address", accounts[0].Email)
	}
	if accounts[0].Assignments[0].PrincipalType != PrincipalGroup {
		t.Errorf("PrincipalType = %q, want GROUP", accounts[0].Assignments[0].PrincipalType)
	}
}

func TestParseStripsUTF8BOM(t *testing.T) {
	in := "\uFEFF" + tsv("aws+dev@example.com\tdev-account\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess")

	if _, err := Parse(strings.NewReader(in)); err != nil {
		t.Fatalf("Parse() returned unexpected error: %v", err)
	}
}

func TestParseRejectsMalformedFile(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty file", in: "", want: "empty"},
		{name: "header only", in: header, want: "no rows"},
		{
			name: "wrong header column",
			in:   "AccountEmail\tAccountName\tOU\tPrincipal\tPrincipalName\tPermissionSetName\nx\n",
			want: "header column 4",
		},
		{
			name: "too few header columns",
			in:   "AccountEmail\tAccountName\n",
			want: "header must have 6 columns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.in))
			if err == nil {
				t.Fatal("Parse() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestParseReportsRowProblems(t *testing.T) {
	tests := []struct {
		name string
		row  string
		want string
	}{
		{
			name: "empty field",
			row:  "aws+dev@example.com\t\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
			want: "AccountName must not be empty",
		},
		{
			name: "invalid email",
			row:  "not-an-email\tdev-account\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
			want: "not a valid address",
		},
		{
			name: "display name form is rejected",
			row:  "Dev <aws+dev@example.com>\tdev-account\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
			want: "bare address",
		},
		{
			name: "malformed OU",
			row:  "aws+dev@example.com\tdev-account\tSandbox\tGROUP\tDevelopers\tAdministratorAccess",
			want: `must be formatted as "Name (ou-xxxx-xxxxxxxx)"`,
		},
		{
			name: "unknown principal type",
			row:  "aws+dev@example.com\tdev-account\tSandbox (ou-abcd-12345678)\tROLE\tDevelopers\tAdministratorAccess",
			want: "PrincipalType must be GROUP or USER",
		},
		{
			name: "wrong column count",
			row:  "aws+dev@example.com\tdev-account\tSandbox (ou-abcd-12345678)",
			want: "expected 6 columns, got 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tsv(tt.row)))
			if err == nil {
				t.Fatal("Parse() = nil error, want error")
			}

			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error is %T, want *ValidationError", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "line 2") {
				t.Errorf("error %q does not point at line 2", err)
			}
		})
	}
}

func TestParseRejectsInconsistentAndDuplicateRows(t *testing.T) {
	tests := []struct {
		name string
		rows []string
		want string
	}{
		{
			name: "same email with a different name",
			rows: []string{
				"aws+dev@example.com\tdev-account\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
				"aws+dev@example.com\tother-name\tSandbox (ou-abcd-12345678)\tUSER\ttaro\tReadOnlyAccess",
			},
			want: `was given the name "dev-account" on line 2`,
		},
		{
			name: "same email with a different OU",
			rows: []string{
				"aws+dev@example.com\tdev-account\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
				"aws+dev@example.com\tdev-account\tStaging (ou-abcd-87654321)\tUSER\ttaro\tReadOnlyAccess",
			},
			want: "was given the OU ou-abcd-12345678 on line 2",
		},
		{
			name: "identical assignment twice",
			rows: []string{
				"aws+dev@example.com\tdev-account\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
				"aws+dev@example.com\tdev-account\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
			},
			want: "duplicate assignment, already given on line 2",
		},
		{
			name: "account name reused by another email",
			rows: []string{
				"aws+dev@example.com\tshared-name\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
				"aws+stg@example.com\tshared-name\tStaging (ou-abcd-87654321)\tGROUP\tDevelopers\tAdministratorAccess",
			},
			want: `account name "shared-name" is already used by aws+dev@example.com on line 2`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tsv(tt.rows...)))
			if err == nil {
				t.Fatal("Parse() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestParseReportsEveryProblemAtOnce(t *testing.T) {
	in := tsv(
		"bad-email\tdev-account\tSandbox (ou-abcd-12345678)\tGROUP\tDevelopers\tAdministratorAccess",
		"aws+stg@example.com\tstg-account\tStaging\tGROUP\tDevelopers\tAdministratorAccess",
		"aws+prd@example.com\tprd-account\tProd (ou-abcd-11112222)\tROLE\tDevelopers\tAdministratorAccess",
	)

	_, err := Parse(strings.NewReader(in))
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error is %T, want *ValidationError", err)
	}
	if len(verr.Problems) != 3 {
		t.Fatalf("got %d problems, want 3: %v", len(verr.Problems), err)
	}
	for i, wantLine := range []int{2, 3, 4} {
		if verr.Problems[i].Line != wantLine {
			t.Errorf("problem %d is on line %d, want %d", i, verr.Problems[i].Line, wantLine)
		}
	}
	if !strings.HasPrefix(err.Error(), "3 problems found:") {
		t.Errorf("error summary = %q, want it to start with \"3 problems found:\"", err)
	}
}

func TestParseFileReportsThePath(t *testing.T) {
	_, err := ParseFile("does-not-exist.tsv")
	if err == nil {
		t.Fatal("ParseFile() = nil error, want error")
	}
	if !strings.Contains(err.Error(), "does-not-exist.tsv") {
		t.Errorf("error %q does not mention the path", err)
	}
}

func TestParseOU(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantName string
		wantID   string
		wantErr  bool
	}{
		{name: "simple", in: "Sandbox (ou-abcd-12345678)", wantName: "Sandbox", wantID: "ou-abcd-12345678"},
		{name: "name with spaces", in: "Dev Accounts (ou-abcd-12345678)", wantName: "Dev Accounts", wantID: "ou-abcd-12345678"},
		{name: "name with parentheses", in: "Dev (old) (ou-abcd-12345678)", wantName: "Dev (old)", wantID: "ou-abcd-12345678"},
		{name: "missing id", in: "Sandbox", wantErr: true},
		{name: "malformed id", in: "Sandbox (ou-12345678)", wantErr: true},
		{name: "uppercase id", in: "Sandbox (ou-ABCD-12345678)", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, id, err := ParseOU(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseOU(%q) = nil error, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOU(%q) returned unexpected error: %v", tt.in, err)
			}
			if name != tt.wantName || id != tt.wantID {
				t.Errorf("ParseOU(%q) = %q, %q, want %q, %q", tt.in, name, id, tt.wantName, tt.wantID)
			}
		})
	}
}

func TestValidationErrorSortsByLine(t *testing.T) {
	verr := &ValidationError{Problems: []Problem{
		{Line: 5, Message: "later"},
		{Line: 2, Message: "earlier"},
	}}

	got := verr.Error()
	if strings.Index(got, "earlier") > strings.Index(got, "later") {
		t.Errorf("problems are not ordered by line:\n%s", got)
	}
	if !strings.HasPrefix(got, "2 problems found:") {
		t.Errorf("summary = %q, want a plural summary", got)
	}
}

func TestValidationErrorSingularSummary(t *testing.T) {
	verr := &ValidationError{Problems: []Problem{{Line: 2, Message: "only one"}}}
	if !strings.HasPrefix(verr.Error(), "1 problem found:") {
		t.Errorf("summary = %q, want a singular summary", verr.Error())
	}
}
