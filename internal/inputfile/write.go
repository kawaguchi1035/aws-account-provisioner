package inputfile

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Write renders accounts back to the TSV format Parse accepts.
func Write(w io.Writer, accounts []Account) error {
	cw := csv.NewWriter(w)
	cw.Comma = '\t'

	if err := cw.Write(Header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	for _, account := range accounts {
		ou := fmt.Sprintf("%s (%s)", account.OUName, account.OUID)
		for _, a := range account.Assignments {
			row := []string{
				account.Email,
				account.Name,
				ou,
				string(a.PrincipalType),
				a.PrincipalName,
				a.PermissionSetName,
			}
			if err := cw.Write(row); err != nil {
				return fmt.Errorf("write row: %w", err)
			}
		}
	}

	cw.Flush()
	return cw.Error()
}

// WriteFile writes accounts to path.
//
// The file is written in full or not at all: it is built in a temporary file
// alongside the destination and renamed into place, so an interrupted write
// cannot leave a half-finished input file behind.
func WriteFile(path string, accounts []Account) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()

	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err = Write(tmp, accounts); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("move temporary file into place: %w", err)
	}
	return nil
}
