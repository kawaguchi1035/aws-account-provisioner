package report

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleRows() []Row {
	return []Row{
		{
			OU:           "Sandbox (ou-abcd-12345678)",
			AccountID:    "111111111111",
			AccountName:  "dev",
			AccountEmail: "aws+dev@example.com",
			Status:       StatusSucceeded,
		},
		{
			OU:           "Staging (ou-abcd-87654321)",
			AccountName:  "stg",
			AccountEmail: "aws+stg@example.com",
			Status:       StatusFailed,
			ErrorMessage: "email already in use",
		},
	}
}

func TestWrite(t *testing.T) {
	var out strings.Builder
	if err := Write(&out, sampleRows()); err != nil {
		t.Fatalf("Write() returned unexpected error: %v", err)
	}

	r := csv.NewReader(strings.NewReader(out.String()))
	r.Comma = '\t'
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("the output is not valid TSV: %v\n%s", err, out.String())
	}

	if len(records) != 3 {
		t.Fatalf("got %d lines, want a header and 2 rows", len(records))
	}
	if strings.Join(records[0], "\t") != strings.Join(Header, "\t") {
		t.Errorf("first line = %v, want the header", records[0])
	}
	if records[1][1] != "111111111111" {
		t.Errorf("AccountID = %q", records[1][1])
	}
	if records[2][4] != StatusFailed || records[2][5] != "email already in use" {
		t.Errorf("failed row = %v, want the status and reason", records[2])
	}
}

func TestWriteHandlesNoRows(t *testing.T) {
	var out strings.Builder
	if err := Write(&out, nil); err != nil {
		t.Fatalf("Write() returned unexpected error: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != strings.Join(Header, "\t") {
		t.Errorf("output = %q, want just the header", got)
	}
}

func TestWriteFileUsesATimestampedName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "results")
	now := time.Date(2026, 9, 20, 18, 15, 30, 0, time.UTC)

	path, err := WriteFile(dir, now, sampleRows())
	if err != nil {
		t.Fatalf("WriteFile() returned unexpected error: %v", err)
	}
	if filepath.Base(path) != "20260920_181530_result.tsv" {
		t.Errorf("file name = %q, want it derived from the timestamp", filepath.Base(path))
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "111111111111") {
		t.Error("the file does not contain the rows")
	}
}

func TestWriteFileCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "results")

	if _, err := WriteFile(dir, time.Now(), sampleRows()); err != nil {
		t.Fatalf("WriteFile() returned unexpected error: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the results directory was not created: %v", err)
	}
}

func TestWriteFileKeepsEarlierRuns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "results")

	first, err := WriteFile(dir, time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC), sampleRows())
	if err != nil {
		t.Fatal(err)
	}
	second, err := WriteFile(dir, time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC), sampleRows())
	if err != nil {
		t.Fatal(err)
	}

	if first == second {
		t.Fatal("two runs wrote to the same path")
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s is missing: %v", path, err)
		}
	}
}
