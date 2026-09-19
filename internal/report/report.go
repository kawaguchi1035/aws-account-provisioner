// Package report records the outcome of a run.
//
// A run can take hours and its output scrolls past, so the result of every
// account is also written to a timestamped file that can be kept, diffed or
// pasted into a ticket.
package report

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Header is the column order of the result file.
var Header = []string{
	"OU",
	"AccountID",
	"AccountName",
	"AccountEmail",
	"Status",
	"ErrorMessage",
}

// Status values used in the result file.
const (
	StatusSucceeded = "SUCCEEDED"
	StatusFailed    = "FAILED"
)

// Row is one account's outcome.
type Row struct {
	OU           string
	AccountID    string
	AccountName  string
	AccountEmail string
	Status       string
	ErrorMessage string
}

// Write renders rows as TSV.
func Write(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	cw.Comma = '\t'

	if err := cw.Write(Header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	for _, r := range rows {
		record := []string{r.OU, r.AccountID, r.AccountName, r.AccountEmail, r.Status, r.ErrorMessage}
		if err := cw.Write(record); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}

	cw.Flush()
	return cw.Error()
}

// WriteFile writes rows to dir/YYYYMMDD_HHMMSS_result.tsv, creating dir if
// needed, and returns the path written.
func WriteFile(dir string, now time.Time, rows []Row) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create results directory: %w", err)
	}

	path := filepath.Join(dir, now.Format("20060102_150405")+"_result.tsv")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create result file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := Write(f, rows); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close result file: %w", err)
	}
	return path, nil
}
