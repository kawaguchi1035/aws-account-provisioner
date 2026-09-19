package config

import (
	"os"
	"testing"
)

// isolate unsets every variable Load reads and moves into an empty working
// directory, so a stray .env or exported variable cannot leak into a test.
//
// t.Setenv is called first purely to register the restore-on-cleanup hook;
// the variable is then genuinely unset. Setting it to "" would not do — godotenv
// treats a variable that is set but empty as already present and will not fill
// it in from .env.
func isolate(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ROOT_ACCOUNT_ID", "ASSUME_ROLE_NAME", "EMAIL_TEMPLATE"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}
	t.Chdir(t.TempDir())
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "minimal configuration applies the default template",
			env:  map[string]string{"ROOT_ACCOUNT_ID": "123456789012"},
			want: Config{
				RootAccountID: "123456789012",
				EmailTemplate: DefaultEmailTemplate,
			},
		},
		{
			name: "every value set",
			env: map[string]string{
				"ROOT_ACCOUNT_ID":  "123456789012",
				"ASSUME_ROLE_NAME": "OrganizationAccountAccessRole",
				"EMAIL_TEMPLATE":   "aws.member+{account_name}@example.com",
			},
			want: Config{
				RootAccountID:  "123456789012",
				AssumeRoleName: "OrganizationAccountAccessRole",
				EmailTemplate:  "aws.member+{account_name}@example.com",
			},
		},
		{
			name: "surrounding whitespace is trimmed",
			env:  map[string]string{"ROOT_ACCOUNT_ID": "  123456789012  "},
			want: Config{
				RootAccountID: "123456789012",
				EmailTemplate: DefaultEmailTemplate,
			},
		},
		{
			name:    "account id is required",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name:    "account id must be 12 digits",
			env:     map[string]string{"ROOT_ACCOUNT_ID": "12345"},
			wantErr: true,
		},
		{
			name:    "account id must be numeric",
			env:     map[string]string{"ROOT_ACCOUNT_ID": "12345678901a"},
			wantErr: true,
		},
		{
			name: "template without the placeholder is rejected",
			env: map[string]string{
				"ROOT_ACCOUNT_ID": "123456789012",
				"EMAIL_TEMPLATE":  "aws@example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolate(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() = nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadReadsDotEnvWithoutOverridingEnvironment(t *testing.T) {
	isolate(t)

	// isolate() already moved us into an empty temporary working directory.
	dotenv := "ROOT_ACCOUNT_ID=999999999999\nASSUME_ROLE_NAME=FromDotEnv\n"
	if err := os.WriteFile(".env", []byte(dotenv), 0o600); err != nil {
		t.Fatal(err)
	}

	// An explicit environment variable must win over the .env value.
	t.Setenv("ROOT_ACCOUNT_ID", "123456789012")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if got.RootAccountID != "123456789012" {
		t.Errorf("RootAccountID = %q, want the environment value to win", got.RootAccountID)
	}
	if got.AssumeRoleName != "FromDotEnv" {
		t.Errorf("AssumeRoleName = %q, want the .env value to be picked up", got.AssumeRoleName)
	}
}

func TestRenderEmail(t *testing.T) {
	tests := []struct {
		name        string
		template    string
		accountName string
		want        string
	}{
		{
			name:        "default template",
			template:    DefaultEmailTemplate,
			accountName: "dev-account",
			want:        "aws+dev-account@example.com",
		},
		{
			name:        "account name is lowercased",
			template:    DefaultEmailTemplate,
			accountName: "Dev-Account",
			want:        "aws+dev-account@example.com",
		},
		{
			name:        "placeholder may appear more than once",
			template:    "{account_name}+{account_name}@example.com",
			accountName: "app",
			want:        "app+app@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{EmailTemplate: tt.template}
			if got := c.RenderEmail(tt.accountName); got != tt.want {
				t.Errorf("RenderEmail(%q) = %q, want %q", tt.accountName, got, tt.want)
			}
		})
	}
}

func TestUsesAssumeRole(t *testing.T) {
	if (Config{}).UsesAssumeRole() {
		t.Error("UsesAssumeRole() = true for empty role name, want false")
	}
	if !(Config{AssumeRoleName: "Role"}).UsesAssumeRole() {
		t.Error("UsesAssumeRole() = false for a set role name, want true")
	}
}
