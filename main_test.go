package main

import (
	"context"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantErr   bool
		wantInit  bool
		wantDry   bool
		wantInput string
	}{
		{
			name:     "init needs no input file",
			args:     []string{"--init"},
			wantInit: true,
		},
		{
			name:      "dry run with input file",
			args:      []string{"--dry-run", "input.tsv"},
			wantDry:   true,
			wantInput: "input.tsv",
		},
		{
			name:      "provision with input file",
			args:      []string{"input.tsv"},
			wantInput: "input.tsv",
		},
		{
			name:    "input file is required",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "only one input file is accepted",
			args:    []string{"a.tsv", "b.tsv"},
			wantErr: true,
		},
		{
			name:    "init and dry-run are mutually exclusive",
			args:    []string{"--init", "--dry-run"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseArgs(%q) = nil error, want error", tt.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs(%q) returned unexpected error: %v", tt.args, err)
			}
			if got.init != tt.wantInit {
				t.Errorf("init = %v, want %v", got.init, tt.wantInit)
			}
			if got.dryRun != tt.wantDry {
				t.Errorf("dryRun = %v, want %v", got.dryRun, tt.wantDry)
			}
			if got.inputPath != tt.wantInput {
				t.Errorf("inputPath = %q, want %q", got.inputPath, tt.wantInput)
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	var out strings.Builder
	if err := run(context.Background(), []string{"--version"}, &out); err != nil {
		t.Fatalf("run() returned unexpected error: %v", err)
	}
	if !strings.HasPrefix(out.String(), "aws-account-provisioner ") {
		t.Errorf("output = %q, want the version line", out.String())
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{n: 0, want: "0 accounts"},
		{n: 1, want: "1 account"},
		{n: 2, want: "2 accounts"},
	}
	for _, tt := range tests {
		if got := pluralize(tt.n, "account"); got != tt.want {
			t.Errorf("pluralize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
