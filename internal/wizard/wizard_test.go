package wizard

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
)

// scriptedPrompter answers prompts from a fixed script, failing the test if the
// wizard asks more questions than were scripted.
type scriptedPrompter struct {
	t        *testing.T
	inputs   []string
	selects  []string
	confirms []bool
	labels   []string // every label asked, in order
	err      error
}

func (p *scriptedPrompter) Input(label string, validate func(string) error) (string, error) {
	p.labels = append(p.labels, label)
	if p.err != nil {
		return "", p.err
	}
	if len(p.inputs) == 0 {
		p.t.Fatalf("unexpected Input(%q): the script is exhausted", label)
	}
	v := p.inputs[0]
	p.inputs = p.inputs[1:]
	if validate != nil {
		if err := validate(v); err != nil {
			return "", err
		}
	}
	return v, nil
}

func (p *scriptedPrompter) Select(label string, items []string) (string, error) {
	p.labels = append(p.labels, label)
	if p.err != nil {
		return "", p.err
	}
	if len(p.selects) == 0 {
		p.t.Fatalf("unexpected Select(%q): the script is exhausted", label)
	}
	v := p.selects[0]
	p.selects = p.selects[1:]
	for _, item := range items {
		if item == v {
			return v, nil
		}
	}
	p.t.Fatalf("Select(%q) was scripted to choose %q, which is not offered: %v", label, v, items)
	return "", nil
}

func (p *scriptedPrompter) Confirm(label string) (bool, error) {
	p.labels = append(p.labels, label)
	if p.err != nil {
		return false, p.err
	}
	if len(p.confirms) == 0 {
		p.t.Fatalf("unexpected Confirm(%q): the script is exhausted", label)
	}
	v := p.confirms[0]
	p.confirms = p.confirms[1:]
	return v, nil
}

type fakeCatalog struct {
	ous            []string
	permissionSets []string
	groups         []string
	users          []string
}

func (c fakeCatalog) OUChoices() []string          { return c.ous }
func (c fakeCatalog) PermissionSetNames() []string { return c.permissionSets }
func (c fakeCatalog) GroupNames() []string         { return c.groups }
func (c fakeCatalog) UserNames() []string          { return c.users }

type fakeEmail struct{}

func (fakeEmail) RenderEmail(name string) string {
	return "aws+" + strings.ToLower(name) + "@example.com"
}

func fullCatalog() fakeCatalog {
	return fakeCatalog{
		ous:            []string{"Sandbox (ou-abcd-12345678)", "Staging (ou-abcd-87654321)"},
		permissionSets: []string{"AdministratorAccess", "ReadOnlyAccess"},
		groups:         []string{"Developers"},
		users:          []string{"taro"},
	}
}

func newWizard(t *testing.T, catalog Catalog, p *scriptedPrompter) Wizard {
	t.Helper()
	return Wizard{Prompter: p, Catalog: catalog, Email: fakeEmail{}, Out: io.Discard}
}

func TestRunCollectsOneAccount(t *testing.T) {
	p := &scriptedPrompter{
		t:        t,
		inputs:   []string{"dev-account"},
		selects:  []string{"Sandbox (ou-abcd-12345678)", "GROUP", "Developers", "AdministratorAccess"},
		confirms: []bool{false, false}, // no more assignments, no more accounts
	}

	accounts, err := newWizard(t, fullCatalog(), p).Run()
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}

	a := accounts[0]
	if a.Name != "dev-account" {
		t.Errorf("Name = %q", a.Name)
	}
	if a.Email != "aws+dev-account@example.com" {
		t.Errorf("Email = %q, want it derived from the template", a.Email)
	}
	if a.OUName != "Sandbox" || a.OUID != "ou-abcd-12345678" {
		t.Errorf("OU = %q / %q", a.OUName, a.OUID)
	}
	if len(a.Assignments) != 1 {
		t.Fatalf("got %d assignments, want 1", len(a.Assignments))
	}
	if a.Assignments[0].PrincipalType != inputfile.PrincipalGroup {
		t.Errorf("PrincipalType = %q", a.Assignments[0].PrincipalType)
	}
}

func TestRunCollectsSeveralAccountsAndAssignments(t *testing.T) {
	p := &scriptedPrompter{
		t:      t,
		inputs: []string{"dev-account", "stg-account"},
		selects: []string{
			"Sandbox (ou-abcd-12345678)", "GROUP", "Developers", "AdministratorAccess",
			"USER", "taro", "ReadOnlyAccess",
			"Staging (ou-abcd-87654321)", "GROUP", "Developers", "AdministratorAccess",
		},
		confirms: []bool{
			true,  // another assignment on the first account
			false, // done with assignments
			true,  // another account
			false, // done with assignments
			false, // done with accounts
		},
	}

	accounts, err := newWizard(t, fullCatalog(), p).Run()
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}
	if len(accounts[0].Assignments) != 2 {
		t.Errorf("first account has %d assignments, want 2", len(accounts[0].Assignments))
	}
	if accounts[1].Name != "stg-account" {
		t.Errorf("second account = %q", accounts[1].Name)
	}
}

func TestRunSkipsDuplicateAssignments(t *testing.T) {
	p := &scriptedPrompter{
		t:      t,
		inputs: []string{"dev-account"},
		selects: []string{
			"Sandbox (ou-abcd-12345678)", "GROUP", "Developers", "AdministratorAccess",
			"GROUP", "Developers", "AdministratorAccess",
		},
		confirms: []bool{true, false, false},
	}

	accounts, err := newWizard(t, fullCatalog(), p).Run()
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if len(accounts[0].Assignments) != 1 {
		t.Errorf("got %d assignments, want the duplicate to be dropped", len(accounts[0].Assignments))
	}
}

func TestRunSkipsPrincipalTypeQuestionWhenOnlyOneKindExists(t *testing.T) {
	catalog := fullCatalog()
	catalog.users = nil

	p := &scriptedPrompter{
		t:        t,
		inputs:   []string{"dev-account"},
		selects:  []string{"Sandbox (ou-abcd-12345678)", "Developers", "AdministratorAccess"},
		confirms: []bool{false, false},
	}

	accounts, err := newWizard(t, catalog, p).Run()
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if accounts[0].Assignments[0].PrincipalType != inputfile.PrincipalGroup {
		t.Errorf("PrincipalType = %q, want GROUP", accounts[0].Assignments[0].PrincipalType)
	}
	for _, label := range p.labels {
		if label == "Assign to" {
			t.Error("the wizard asked which principal type to use even though only groups exist")
		}
	}
}

func TestAccountNameValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "simple", input: "dev-account"},
		{name: "dots and underscores", input: "dev.account_1"},
		{name: "empty", input: "", wantErr: true},
		{name: "leading hyphen", input: "-dev", wantErr: true},
		{name: "space", input: "dev account", wantErr: true},
		{name: "slash", input: "dev/account", wantErr: true},
		{name: "too long", input: strings.Repeat("a", 51), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &scriptedPrompter{
				t:        t,
				inputs:   []string{tt.input},
				selects:  []string{"Sandbox (ou-abcd-12345678)", "GROUP", "Developers", "AdministratorAccess"},
				confirms: []bool{false, false},
			}
			_, err := newWizard(t, fullCatalog(), p).Run()
			if tt.wantErr != (err != nil) {
				t.Fatalf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunRejectsAReusedAccountName(t *testing.T) {
	p := &scriptedPrompter{
		t:      t,
		inputs: []string{"dev-account", "dev-account"},
		selects: []string{
			"Sandbox (ou-abcd-12345678)", "GROUP", "Developers", "AdministratorAccess",
		},
		confirms: []bool{false, true},
	}

	_, err := newWizard(t, fullCatalog(), p).Run()
	if err == nil {
		t.Fatal("Run() = nil error, want the reused name to be rejected")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Errorf("error %q does not explain the problem", err)
	}
}

func TestRunRejectsAnEmptyCatalog(t *testing.T) {
	tests := []struct {
		name    string
		catalog fakeCatalog
		want    string
	}{
		{
			name:    "no OUs",
			catalog: fakeCatalog{permissionSets: []string{"AdministratorAccess"}, groups: []string{"Developers"}},
			want:    "no organizational units",
		},
		{
			name:    "no permission sets",
			catalog: fakeCatalog{ous: []string{"Sandbox (ou-abcd-12345678)"}, groups: []string{"Developers"}},
			want:    "no permission sets",
		},
		{
			name: "no principals",
			catalog: fakeCatalog{
				ous:            []string{"Sandbox (ou-abcd-12345678)"},
				permissionSets: []string{"AdministratorAccess"},
			},
			want: "no groups or users",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &scriptedPrompter{t: t}
			_, err := newWizard(t, tt.catalog, p).Run()
			if err == nil {
				t.Fatal("Run() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
			if len(p.labels) != 0 {
				t.Errorf("the wizard asked %v before noticing the catalog was empty", p.labels)
			}
		})
	}
}

func TestRunPropagatesPrompterErrors(t *testing.T) {
	want := errors.New("interrupted")
	p := &scriptedPrompter{t: t, err: want}

	_, err := newWizard(t, fullCatalog(), p).Run()
	if !errors.Is(err, want) {
		t.Errorf("Run() error = %v, want it to propagate %v", err, want)
	}
}

func TestSummarize(t *testing.T) {
	var out strings.Builder
	Summarize(&out, []inputfile.Account{{
		Email:  "aws+dev@example.com",
		Name:   "dev-account",
		OUName: "Sandbox",
		OUID:   "ou-abcd-12345678",
		Assignments: []inputfile.Assignment{{
			PrincipalType:     inputfile.PrincipalGroup,
			PrincipalName:     "Developers",
			PermissionSetName: "AdministratorAccess",
		}},
	}})

	for _, want := range []string{"dev-account", "aws+dev@example.com", "ou-abcd-12345678", "Developers", "AdministratorAccess"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary does not mention %q:\n%s", want, out.String())
		}
	}
}
