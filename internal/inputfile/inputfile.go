// Package inputfile reads and validates the TSV that drives a provisioning run.
//
// One row is one assignment. Rows sharing an account email are grouped into a
// single Account, so the account is created once and every assignment for it is
// applied afterwards.
package inputfile

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
)

// PrincipalType distinguishes a group assignment from a user assignment.
type PrincipalType string

// Supported principal types.
const (
	PrincipalGroup PrincipalType = "GROUP"
	PrincipalUser  PrincipalType = "USER"
)

// Header is the exact column order the input file must use.
var Header = []string{
	"AccountEmail",
	"AccountName",
	"OU",
	"PrincipalType",
	"PrincipalName",
	"PermissionSetName",
}

// Assignment is one permission set granted to one principal.
type Assignment struct {
	PrincipalType     PrincipalType
	PrincipalName     string
	PermissionSetName string

	// Line is the 1-based line in the source file, used in error messages.
	Line int
}

// Account is one account to create, with every assignment that belongs to it.
type Account struct {
	Email       string
	Name        string
	OUName      string
	OUID        string
	Assignments []Assignment

	// Line is the line the account was first seen on.
	Line int
}

// ParseFile reads and validates the TSV at path.
func ParseFile(path string) ([]Account, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open input file: %w", err)
	}
	defer func() { _ = f.Close() }()

	accounts, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return accounts, nil
}

// Parse reads and validates a TSV stream.
//
// Every problem found is collected and returned together as a *ValidationError,
// so a single run surfaces all of them rather than stopping at the first.
func Parse(r io.Reader) ([]Account, error) {
	records, err := readTSV(r)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("input file is empty")
	}
	if err := checkHeader(records[0]); err != nil {
		return nil, err
	}
	if len(records) == 1 {
		return nil, fmt.Errorf("input file has a header but no rows")
	}
	return buildAccounts(records[1:])
}

func readTSV(r io.Reader) ([][]string, error) {
	reader := csv.NewReader(r)
	reader.Comma = '\t'
	// Fields are validated individually, so let the reader accept ragged rows
	// and report the column count as a normal validation problem instead.
	reader.FieldsPerRecord = -1

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read TSV: %w", err)
	}
	if len(records) > 0 && len(records[0]) > 0 {
		// Spreadsheet exports often prepend a UTF-8 BOM to the first cell.
		records[0][0] = strings.TrimPrefix(records[0][0], "\uFEFF")
	}
	return records, nil
}

func checkHeader(got []string) error {
	if len(got) != len(Header) {
		return fmt.Errorf("header must have %d columns (%s), got %d",
			len(Header), strings.Join(Header, ", "), len(got))
	}
	for i, want := range Header {
		if strings.TrimSpace(got[i]) != want {
			return fmt.Errorf("header column %d must be %q, got %q", i+1, want, got[i])
		}
	}
	return nil
}

// buildAccounts validates each row and groups them by account email, preserving
// the order in which accounts first appear.
func buildAccounts(rows [][]string) ([]Account, error) {
	var (
		verr     ValidationError
		order    []string
		byEmail  = map[string]*Account{}
		seenKeys = map[string]int{}
	)

	for i, row := range rows {
		line := i + 2 // 1-based, and the header occupies line 1.

		if len(row) != len(Header) {
			verr.add(line, fmt.Errorf("expected %d columns, got %d", len(Header), len(row)))
			continue
		}

		parsed, err := parseRow(row)
		if err != nil {
			verr.add(line, err)
			continue
		}

		key := strings.Join([]string{
			parsed.email, string(parsed.principalType), parsed.principalName, parsed.permissionSet,
		}, "\x00")
		if first, dup := seenKeys[key]; dup {
			verr.add(line, fmt.Errorf("duplicate assignment, already given on line %d", first))
			continue
		}
		seenKeys[key] = line

		account, ok := byEmail[parsed.email]
		if !ok {
			account = &Account{
				Email:  parsed.email,
				Name:   parsed.name,
				OUName: parsed.ouName,
				OUID:   parsed.ouID,
				Line:   line,
			}
			byEmail[parsed.email] = account
			order = append(order, parsed.email)
		} else if err := checkConsistency(account, parsed); err != nil {
			verr.add(line, err)
			continue
		}

		account.Assignments = append(account.Assignments, Assignment{
			PrincipalType:     parsed.principalType,
			PrincipalName:     parsed.principalName,
			PermissionSetName: parsed.permissionSet,
			Line:              line,
		})
	}

	checkNameReuse(byEmail, order, &verr)

	if verr.hasProblems() {
		return nil, &verr
	}

	accounts := make([]Account, 0, len(order))
	for _, email := range order {
		accounts = append(accounts, *byEmail[email])
	}
	return accounts, nil
}

// checkConsistency rejects rows that describe the same account differently.
// Letting these through would silently create the account from whichever row
// happened to come first.
func checkConsistency(existing *Account, got parsedRow) error {
	if existing.Name != got.name {
		return fmt.Errorf("account %s was given the name %q on line %d, got %q",
			got.email, existing.Name, existing.Line, got.name)
	}
	if existing.OUID != got.ouID {
		return fmt.Errorf("account %s was given the OU %s on line %d, got %s",
			got.email, existing.OUID, existing.Line, got.ouID)
	}
	return nil
}

// checkNameReuse flags the same account name being used for two different
// emails. AWS permits it, but it makes the resulting accounts hard to tell apart.
func checkNameReuse(byEmail map[string]*Account, order []string, verr *ValidationError) {
	firstByName := map[string]*Account{}
	for _, email := range order {
		account := byEmail[email]
		if first, seen := firstByName[account.Name]; seen {
			verr.add(account.Line, fmt.Errorf("account name %q is already used by %s on line %d",
				account.Name, first.Email, first.Line))
			continue
		}
		firstByName[account.Name] = account
	}
}
