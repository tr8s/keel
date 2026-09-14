package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

// KeelChangeBaseLabel - image label holding the revision the tag pointed at before the build, where the commit list
// of the change starts. It may be empty.
const KeelChangeBaseLabel = "sh.keel.change.base"

// KeelChangeCountLabel - image label holding the number of commits from the base revision to the revision of the
// image, as an integer string
const KeelChangeCountLabel = "sh.keel.change.count"

// KeelChangeCommitsLabel - image label holding the newest commits of the change, as standard base64 of a JSON array
// of {"sha": "...", "subject": "..."}, newest first
const KeelChangeCommitsLabel = "sh.keel.change.commits"

// MaxChangeCommits - how many commits of a change are kept and shown
const MaxChangeCommits = 5

// ChangeCommit - a commit of the change behind an update
type ChangeCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// ChangeCommits is stored as a JSON blob
type ChangeCommits []ChangeCommit

func (c ChangeCommits) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *ChangeCommits) Scan(src interface{}) error {
	var source []byte
	switch value := src.(type) {
	case []byte:
		source = value
	case string:
		source = []byte(value)
	default:
		return errors.New("type assertion .([]byte) failed.")
	}
	return json.Unmarshal(source, c)
}
