package sql

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDataDir(t *testing.T) {
	writable := t.TempDir()
	if err := CheckDataDir(writable); err != nil {
		t.Errorf("expected %s to be writable, got %v", writable, err)
	}
	if entries, _ := os.ReadDir(writable); len(entries) != 0 {
		t.Errorf("expected the check to leave no file behind, got %d entries", len(entries))
	}

	if err := CheckDataDir(filepath.Join(writable, "missing")); err == nil {
		t.Error("expected a missing data directory to be reported")
	}

	file := filepath.Join(writable, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDataDir(file); err == nil {
		t.Error("expected a data directory that is a file to be reported")
	}
}

func TestCheckDataDirReadOnly(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write to read-only directories")
	}
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(readOnly, 0o755) })

	if err := CheckDataDir(readOnly); err == nil {
		t.Error("expected a read-only data directory to be reported")
	}
}
