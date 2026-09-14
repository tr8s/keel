package kubernetes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/keel-hq/keel/types"

	log "github.com/sirupsen/logrus"
)

// describeCommits records the commits of the change from the sh.keel.change.* labels of the new image. A commit
// list that can not be decoded is left out, so that the approval shows the single commit subject instead.
func describeCommits(approval *types.Approval, labels map[string]string) {
	approval.ChangeBase = labels[types.KeelChangeBaseLabel]
	if count, err := strconv.Atoi(strings.TrimSpace(labels[types.KeelChangeCountLabel])); err == nil && count > 0 {
		approval.ChangeCount = count
	}

	value := labels[types.KeelChangeCommitsLabel]
	if value == "" {
		return
	}
	commits, err := decodeChangeCommits(value)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
			"label": types.KeelChangeCommitsLabel,
		}).Debug("provider.kubernetes: ignoring the commit list of the image, the approval shows the commit subject")
		return
	}
	approval.ChangeCommits = commits
}

// decodeChangeCommits decodes the sh.keel.change.commits label: standard base64 of a JSON array of commits, newest
// first. Commits without a sha are dropped and at most types.MaxChangeCommits are kept.
func decodeChangeCommits(value string) (types.ChangeCommits, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}

	var commits types.ChangeCommits
	if err := json.Unmarshal(decoded, &commits); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}

	var valid types.ChangeCommits
	for _, commit := range commits {
		if commit.SHA == "" {
			continue
		}
		valid = append(valid, commit)
		if len(valid) == types.MaxChangeCommits {
			break
		}
	}
	return valid, nil
}
