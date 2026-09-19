package inputfile

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
)

// ouPattern matches the "Name (ou-xxxx-xxxxxxxx)" form the OU column uses.
// The identifier shape follows the AWS Organizations documentation: "ou-",
// 4-32 lowercase alphanumerics, "-", then 8-32 lowercase alphanumerics.
var ouPattern = regexp.MustCompile(`^(.+?)\s*\((ou-[0-9a-z]{4,32}-[0-9a-z]{8,32})\)$`)

// parsedRow is a validated row, ready to be folded into an Account.
type parsedRow struct {
	email         string
	name          string
	ouName        string
	ouID          string
	principalType PrincipalType
	principalName string
	permissionSet string
}

func parseRow(row []string) (parsedRow, error) {
	fields := make([]string, len(row))
	for i, v := range row {
		fields[i] = strings.TrimSpace(v)
	}

	for i, v := range fields {
		if v == "" {
			return parsedRow{}, fmt.Errorf("%s must not be empty", Header[i])
		}
	}

	email, err := parseEmail(fields[0])
	if err != nil {
		return parsedRow{}, err
	}

	ouName, ouID, err := ParseOU(fields[2])
	if err != nil {
		return parsedRow{}, err
	}

	principalType, err := parsePrincipalType(fields[3])
	if err != nil {
		return parsedRow{}, err
	}

	return parsedRow{
		email:         email,
		name:          fields[1],
		ouName:        ouName,
		ouID:          ouID,
		principalType: principalType,
		principalName: fields[4],
		permissionSet: fields[5],
	}, nil
}

func parseEmail(v string) (string, error) {
	addr, err := mail.ParseAddress(v)
	if err != nil {
		return "", fmt.Errorf("AccountEmail %q is not a valid address", v)
	}
	// Reject the "Display Name <addr>" form: this value becomes the account's
	// root email and must be the bare address.
	if addr.Address != v {
		return "", fmt.Errorf("AccountEmail must be a bare address, got %q", v)
	}
	return addr.Address, nil
}

// ParseOU splits "Name (ou-xxxx-xxxxxxxx)" into its name and identifier.
func ParseOU(v string) (name, id string, err error) {
	m := ouPattern.FindStringSubmatch(v)
	if m == nil {
		return "", "", fmt.Errorf("OU %q must be formatted as \"Name (ou-xxxx-xxxxxxxx)\"", v)
	}
	return strings.TrimSpace(m[1]), m[2], nil
}

func parsePrincipalType(v string) (PrincipalType, error) {
	switch PrincipalType(strings.ToUpper(v)) {
	case PrincipalGroup:
		return PrincipalGroup, nil
	case PrincipalUser:
		return PrincipalUser, nil
	default:
		return "", fmt.Errorf("PrincipalType must be %s or %s, got %q", PrincipalGroup, PrincipalUser, v)
	}
}
