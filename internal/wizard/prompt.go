package wizard

import (
	"strings"

	"github.com/manifoldco/promptui"
)

// Prompter is the terminal interaction the wizard needs.
//
// It exists so the wizard's logic can be driven by a scripted implementation in
// tests; promptui itself requires a real terminal.
type Prompter interface {
	// Input asks for free text. validate may be nil.
	Input(label string, validate func(string) error) (string, error)

	// Select asks the user to pick one of items.
	Select(label string, items []string) (string, error)

	// Confirm asks a yes/no question.
	Confirm(label string) (bool, error)
}

// TerminalPrompter is the promptui-backed Prompter used in real runs.
type TerminalPrompter struct{}

// Input asks for free text.
func (TerminalPrompter) Input(label string, validate func(string) error) (string, error) {
	p := promptui.Prompt{Label: label, Validate: validate}
	return p.Run()
}

// Select asks the user to pick one of items, with incremental search so long
// lists of users or permission sets stay usable.
func (TerminalPrompter) Select(label string, items []string) (string, error) {
	p := promptui.Select{
		Label:             label,
		Items:             items,
		Size:              12,
		StartInSearchMode: len(items) > 12,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(items[index]), strings.ToLower(input))
		},
	}
	_, choice, err := p.Run()
	return choice, err
}

// Confirm asks a yes/no question, defaulting to no.
func (TerminalPrompter) Confirm(label string) (bool, error) {
	p := promptui.Prompt{Label: label, IsConfirm: true}
	answer, err := p.Run()
	if err != nil {
		// promptui reports a "no" answer as ErrAbort rather than a value.
		if err == promptui.ErrAbort {
			return false, nil
		}
		return false, err
	}
	return strings.EqualFold(answer, "y"), nil
}
