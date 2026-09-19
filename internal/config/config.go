// Package config loads the tool's settings from environment variables,
// optionally seeded by a .env file in the working directory.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/joho/godotenv"
)

// DefaultEmailTemplate is used by the interactive wizard when EMAIL_TEMPLATE is unset.
const DefaultEmailTemplate = "aws+{account_name}@example.com"

// accountNamePlaceholder is substituted with the lowercased account name.
const accountNamePlaceholder = "{account_name}"

var accountIDPattern = regexp.MustCompile(`^\d{12}$`)

// Config holds every setting the tool reads from the environment.
type Config struct {
	// RootAccountID is the Organizations management account. Required.
	RootAccountID string

	// AssumeRoleName, when set, names a role in RootAccountID to assume before
	// calling any API. When empty, the profile's own credentials are used.
	AssumeRoleName string

	// EmailTemplate derives root email addresses in the wizard.
	EmailTemplate string
}

// Load reads the configuration. A .env file in the working directory is loaded
// first if present, but never overrides variables already set in the environment.
func Load() (Config, error) {
	// A missing .env is not an error — the environment alone is a valid source.
	_ = godotenv.Load(".env")

	cfg := Config{
		RootAccountID:  strings.TrimSpace(os.Getenv("ROOT_ACCOUNT_ID")),
		AssumeRoleName: strings.TrimSpace(os.Getenv("ASSUME_ROLE_NAME")),
		EmailTemplate:  strings.TrimSpace(os.Getenv("EMAIL_TEMPLATE")),
	}

	if cfg.EmailTemplate == "" {
		cfg.EmailTemplate = DefaultEmailTemplate
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.RootAccountID == "" {
		return fmt.Errorf("ROOT_ACCOUNT_ID is required")
	}
	if !accountIDPattern.MatchString(c.RootAccountID) {
		return fmt.Errorf("ROOT_ACCOUNT_ID must be 12 digits, got %q", c.RootAccountID)
	}
	if !strings.Contains(c.EmailTemplate, accountNamePlaceholder) {
		return fmt.Errorf("EMAIL_TEMPLATE must contain %s, got %q", accountNamePlaceholder, c.EmailTemplate)
	}
	return nil
}

// RenderEmail substitutes the account name into the configured template.
func (c Config) RenderEmail(accountName string) string {
	return strings.ReplaceAll(c.EmailTemplate, accountNamePlaceholder, strings.ToLower(accountName))
}

// UsesAssumeRole reports whether an AssumeRole step is configured.
func (c Config) UsesAssumeRole() bool {
	return c.AssumeRoleName != ""
}
