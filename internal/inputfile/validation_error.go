package inputfile

import (
	"fmt"
	"sort"
	"strings"
)

// Problem is one validation failure, tied to the line it was found on.
type Problem struct {
	Line    int
	Message string
}

// ValidationError collects every problem found in one pass over the input.
//
// Creating an AWS account cannot be undone, so the tool reports all of them at
// once rather than making the operator fix and re-run one line at a time.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) add(line int, err error) {
	e.Problems = append(e.Problems, Problem{Line: line, Message: err.Error()})
}

func (e *ValidationError) hasProblems() bool {
	return len(e.Problems) > 0
}

// Error renders every problem, one per line, ordered by line number.
func (e *ValidationError) Error() string {
	problems := make([]Problem, len(e.Problems))
	copy(problems, e.Problems)
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Line < problems[j].Line })

	var b strings.Builder
	fmt.Fprintf(&b, "%s found:", pluralize(len(problems), "problem"))
	for _, p := range problems {
		fmt.Fprintf(&b, "\n  line %d: %s", p.Line, p.Message)
	}
	return b.String()
}

func pluralize(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
