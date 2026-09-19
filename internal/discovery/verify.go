package discovery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kawaguchi1035/aws-account-provisioner/internal/inputfile"
)

// PermissionSetARN returns the ARN of a permission set by name.
func (c *Catalog) PermissionSetARN(name string) (string, bool) {
	arn, ok := c.permissionSets[name]
	return arn, ok
}

// PrincipalID returns the Identity Store ID of a group or user by name.
func (c *Catalog) PrincipalID(t inputfile.PrincipalType, name string) (string, bool) {
	switch t {
	case inputfile.PrincipalGroup:
		id, ok := c.groups[name]
		return id, ok
	case inputfile.PrincipalUser:
		id, ok := c.users[name]
		return id, ok
	default:
		return "", false
	}
}

// OUName returns the name of an organizational unit by ID.
func (c *Catalog) OUName(id string) (string, bool) {
	name, ok := c.ous[id]
	return name, ok
}

// Verify checks that every OU, permission set, group and user referenced by the
// input exists in AWS.
//
// This is the second half of --dry-run: inputfile validates the file's shape,
// and this validates what the file points at. Both run before anything is
// created, and every problem is reported at once.
func (c *Catalog) Verify(accounts []inputfile.Account) error {
	var problems []string

	for _, account := range accounts {
		if _, ok := c.OUName(account.OUID); !ok {
			problems = append(problems, fmt.Sprintf(
				"line %d: OU %s does not exist in this organization", account.Line, account.OUID))
		} else if name, _ := c.OUName(account.OUID); name != account.OUName {
			// The identifier wins — the name is there to make the file readable
			// — but a mismatch usually means the file is stale.
			problems = append(problems, fmt.Sprintf(
				"line %d: OU %s is named %q in AWS, but the file says %q",
				account.Line, account.OUID, name, account.OUName))
		}

		for _, a := range account.Assignments {
			if _, ok := c.PermissionSetARN(a.PermissionSetName); !ok {
				problems = append(problems, fmt.Sprintf(
					"line %d: permission set %q does not exist%s",
					a.Line, a.PermissionSetName, suggest(a.PermissionSetName, c.permissionSetNames())))
			}
			if _, ok := c.PrincipalID(a.PrincipalType, a.PrincipalName); !ok {
				problems = append(problems, fmt.Sprintf(
					"line %d: %s %q does not exist in IAM Identity Center%s",
					a.Line, strings.ToLower(string(a.PrincipalType)), a.PrincipalName,
					suggest(a.PrincipalName, c.principalNames(a.PrincipalType))))
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s found in AWS:\n  %s", pluralize(len(problems), "problem"), strings.Join(problems, "\n  "))
}

func (c *Catalog) permissionSetNames() []string { return sortedKeys(c.permissionSets) }

func (c *Catalog) principalNames(t inputfile.PrincipalType) []string {
	if t == inputfile.PrincipalGroup {
		return sortedKeys(c.groups)
	}
	return sortedKeys(c.users)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// suggest offers the closest candidate when a name looks like a typo, so the
// operator does not have to go hunting in the console.
func suggest(got string, candidates []string) string {
	best, bestDist := "", -1
	for _, c := range candidates {
		d := editDistance(strings.ToLower(got), strings.ToLower(c))
		if bestDist < 0 || d < bestDist {
			best, bestDist = c, d
		}
	}
	// Only suggest when the names are genuinely close; an unrelated nearest
	// match is noise.
	if best == "" || bestDist > maxSuggestDistance(got) {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

func maxSuggestDistance(s string) int {
	switch n := len([]rune(s)); {
	case n <= 4:
		return 1
	case n <= 10:
		return 2
	default:
		return 3
	}
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)

	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

func pluralize(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
