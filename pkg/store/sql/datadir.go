package sql

import (
	"os"
)

// CheckDataDir returns why files can not be created in the data directory, nil when they can. SQLite reports a
// data directory it can not write to, ie: a volume owned by root, only as "unable to open database file".
func CheckDataDir(dir string) error {
	file, err := os.CreateTemp(dir, ".keel-write-check-*")
	if err != nil {
		return err
	}
	name := file.Name()
	file.Close()
	os.Remove(name)
	return nil
}
