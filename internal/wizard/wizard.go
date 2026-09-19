// Package wizard builds an input file interactively, offering only the OUs,
// permission sets, groups and users that actually exist in AWS.
//
// Choosing from a list rather than typing names is the point: it removes the
// class of mistakes that would otherwise only surface at validation time.
package wizard

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
)

// accountNamePattern keeps names to what Control Tower accepts comfortably and
// what makes a sane email local part.
var accountNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,49}$`)

// Catalog is the AWS-side state the wizard offers as choices.
type Catalog interface {
	// OUChoices returns organizational units as "Name (ou-xxxx-xxxxxxxx)".
	OUChoices() []string
	PermissionSetNames() []string
	GroupNames() []string
	UserNames() []string
}

// EmailRenderer derives a root email address from an account name.
type EmailRenderer interface {
	RenderEmail(accountName string) string
}

// Wizard collects accounts through a Prompter.
type Wizard struct {
	Prompter Prompter
	Catalog  Catalog
	Email    EmailRenderer
	Out      io.Writer
}

// Run collects one or more accounts. It returns at least one account, or an
// error if the user aborted.
func (w Wizard) Run() ([]inputfile.Account, error) {
	if err := w.checkCatalog(); err != nil {
		return nil, err
	}

	var accounts []inputfile.Account
	taken := map[string]bool{}

	for {
		account, err := w.collectAccount(taken)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
		taken[account.Name] = true

		more, err := w.Prompter.Confirm("Add another account?")
		if err != nil {
			return nil, err
		}
		if !more {
			return accounts, nil
		}
	}
}

// checkCatalog fails early rather than presenting an empty list mid-way.
func (w Wizard) checkCatalog() error {
	if len(w.Catalog.OUChoices()) == 0 {
		return errors.New("no organizational units found; create one before provisioning accounts")
	}
	if len(w.Catalog.PermissionSetNames()) == 0 {
		return errors.New("no permission sets found in IAM Identity Center")
	}
	if len(w.Catalog.GroupNames()) == 0 && len(w.Catalog.UserNames()) == 0 {
		return errors.New("no groups or users found in IAM Identity Center")
	}
	return nil
}

func (w Wizard) collectAccount(taken map[string]bool) (inputfile.Account, error) {
	name, err := w.Prompter.Input("Account name", func(v string) error {
		v = strings.TrimSpace(v)
		if !accountNamePattern.MatchString(v) {
			return errors.New("use letters, digits, dot, underscore or hyphen (1-50 characters, starting with a letter or digit)")
		}
		if taken[v] {
			return fmt.Errorf("account name %q was already used in this session", v)
		}
		return nil
	})
	if err != nil {
		return inputfile.Account{}, err
	}
	name = strings.TrimSpace(name)

	email := w.Email.RenderEmail(name)
	w.printf("  root email: %s\n", email)

	ouChoice, err := w.Prompter.Select("Organizational unit", w.Catalog.OUChoices())
	if err != nil {
		return inputfile.Account{}, err
	}
	ouName, ouID, err := inputfile.ParseOU(ouChoice)
	if err != nil {
		return inputfile.Account{}, err
	}

	assignments, err := w.collectAssignments()
	if err != nil {
		return inputfile.Account{}, err
	}

	return inputfile.Account{
		Email:       email,
		Name:        name,
		OUName:      ouName,
		OUID:        ouID,
		Assignments: assignments,
	}, nil
}

// collectAssignments loops until at least one assignment exists and the user is
// done. An account with no assignment would be created but unreachable through
// Identity Center, which is never what someone means.
func (w Wizard) collectAssignments() ([]inputfile.Assignment, error) {
	var assignments []inputfile.Assignment
	seen := map[inputfile.Assignment]bool{}

	for {
		assignment, err := w.collectAssignment()
		if err != nil {
			return nil, err
		}
		if seen[assignment] {
			w.printf("  already added, skipping\n")
		} else {
			seen[assignment] = true
			assignments = append(assignments, assignment)
		}

		more, err := w.Prompter.Confirm("Add another assignment to this account?")
		if err != nil {
			return nil, err
		}
		if !more {
			return assignments, nil
		}
	}
}

func (w Wizard) collectAssignment() (inputfile.Assignment, error) {
	principalType, err := w.selectPrincipalType()
	if err != nil {
		return inputfile.Assignment{}, err
	}

	var names []string
	if principalType == inputfile.PrincipalGroup {
		names = w.Catalog.GroupNames()
	} else {
		names = w.Catalog.UserNames()
	}

	principal, err := w.Prompter.Select(string(principalType), names)
	if err != nil {
		return inputfile.Assignment{}, err
	}

	permissionSet, err := w.Prompter.Select("Permission set", w.Catalog.PermissionSetNames())
	if err != nil {
		return inputfile.Assignment{}, err
	}

	return inputfile.Assignment{
		PrincipalType:     principalType,
		PrincipalName:     principal,
		PermissionSetName: permissionSet,
	}, nil
}

// selectPrincipalType skips the question when only one kind of principal exists.
func (w Wizard) selectPrincipalType() (inputfile.PrincipalType, error) {
	hasGroups := len(w.Catalog.GroupNames()) > 0
	hasUsers := len(w.Catalog.UserNames()) > 0

	switch {
	case hasGroups && !hasUsers:
		return inputfile.PrincipalGroup, nil
	case hasUsers && !hasGroups:
		return inputfile.PrincipalUser, nil
	}

	choice, err := w.Prompter.Select("Assign to", []string{
		string(inputfile.PrincipalGroup),
		string(inputfile.PrincipalUser),
	})
	if err != nil {
		return "", err
	}
	return inputfile.PrincipalType(choice), nil
}

// Summarize prints what will be written, so the file is reviewed before it is
// used against AWS.
func Summarize(out io.Writer, accounts []inputfile.Account) {
	for _, a := range accounts {
		_, _ = fmt.Fprintf(out, "\n%s (%s)\n", a.Name, a.Email)
		_, _ = fmt.Fprintf(out, "  OU: %s (%s)\n", a.OUName, a.OUID)
		for _, as := range a.Assignments {
			_, _ = fmt.Fprintf(out, "  %-5s %-24s %s\n", as.PrincipalType, as.PrincipalName, as.PermissionSetName)
		}
	}
}

func (w Wizard) printf(format string, a ...any) {
	if w.Out == nil {
		return
	}
	_, _ = fmt.Fprintf(w.Out, format, a...)
}
