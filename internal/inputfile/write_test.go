package inputfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sampleAccounts() []Account {
	return []Account{
		{
			Email:  "aws+dev@example.com",
			Name:   "dev-account",
			OUName: "Sandbox",
			OUID:   "ou-abcd-12345678",
			Assignments: []Assignment{
				{PrincipalType: PrincipalGroup, PrincipalName: "Developers", PermissionSetName: "AdministratorAccess"},
				{PrincipalType: PrincipalUser, PrincipalName: "taro", PermissionSetName: "ReadOnlyAccess"},
			},
		},
		{
			Email:  "aws+stg@example.com",
			Name:   "stg-account",
			OUName: "Staging",
			OUID:   "ou-abcd-87654321",
			Assignments: []Assignment{
				{PrincipalType: PrincipalGroup, PrincipalName: "Developers", PermissionSetName: "AdministratorAccess"},
			},
		},
	}
}

func TestWriteThenParseRoundTrips(t *testing.T) {
	var out strings.Builder
	if err := Write(&out, sampleAccounts()); err != nil {
		t.Fatalf("Write() returned unexpected error: %v", err)
	}

	got, err := Parse(strings.NewReader(out.String()))
	if err != nil {
		t.Fatalf("Parse() rejected what Write() produced: %v\n%s", err, out.String())
	}

	want := sampleAccounts()
	if len(got) != len(want) {
		t.Fatalf("got %d accounts, want %d", len(got), len(want))
	}
	for i := range want {
		// Line numbers only exist after parsing, so compare the rest.
		got[i].Line = 0
		for j := range got[i].Assignments {
			got[i].Assignments[j].Line = 0
		}
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("account %d round-tripped as %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestWriteStartsWithTheHeader(t *testing.T) {
	var out strings.Builder
	if err := Write(&out, sampleAccounts()); err != nil {
		t.Fatalf("Write() returned unexpected error: %v", err)
	}

	firstLine := strings.SplitN(out.String(), "\n", 2)[0]
	if firstLine != strings.Join(Header, "\t") {
		t.Errorf("first line = %q, want the header", firstLine)
	}
}

func TestWriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.tsv")
	if err := WriteFile(path, sampleAccounts()); err != nil {
		t.Fatalf("WriteFile() returned unexpected error: %v", err)
	}

	accounts, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() rejected the written file: %v", err)
	}
	if len(accounts) != 2 {
		t.Errorf("got %d accounts, want 2", len(accounts))
	}
}

func TestWriteFileLeavesNoTemporaryFilesBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.tsv")
	if err := WriteFile(path, sampleAccounts()); err != nil {
		t.Fatalf("WriteFile() returned unexpected error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "input.tsv" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("directory contains %v, want only input.tsv", names)
	}
}

func TestWriteFileReplacesAnExistingFileAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.tsv")
	if err := os.WriteFile(path, []byte("stale content"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(path, sampleAccounts()); err != nil {
		t.Fatalf("WriteFile() returned unexpected error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "stale") {
		t.Error("the previous content survived the write")
	}
}

func TestWriteFileFailsOnAnUnwritableDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "input.tsv")
	if err := WriteFile(path, sampleAccounts()); err == nil {
		t.Fatal("WriteFile() = nil error, want an error for a missing directory")
	}
}
