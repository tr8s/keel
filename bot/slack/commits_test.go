package slack

import (
	"fmt"
	"strings"
	"testing"

	"github.com/keel-hq/keel/types"
)

func commitList(count int) types.ChangeCommits {
	var commits types.ChangeCommits
	for i := 1; i <= count && i <= types.MaxChangeCommits; i++ {
		commits = append(commits, types.ChangeCommit{
			SHA:     fmt.Sprintf("%d96a024c85d9e0f1a2b3c4d5e6f708192a3b4c5d", i),
			Subject: fmt.Sprintf("Commit %d", i),
		})
	}
	return commits
}

func TestCompactCommitList(t *testing.T) {
	const subjectLine = `"text":"Label images with their commit and source","type":"mrkdwn"`

	tests := []struct {
		name     string
		approval func() *types.Approval
		text     string
		contains []string
		excludes []string
	}{
		{
			name: "single commit keeps the subject",
			approval: func() *types.Approval {
				req := groupApproval()
				req.ChangeCount = 1
				req.ChangeCommits = commitList(1)
				return req
			},
			text:     "Deploy trackeid (api, portal) 8f714c1: Label images with their commit and source",
			contains: []string{subjectLine},
			excludes: []string{"• "},
		},
		{
			name: "several commits replace the subject",
			approval: func() *types.Approval {
				req := groupApproval()
				req.ChangeCount = 3
				req.ChangeCommits = commitList(3)
				return req
			},
			text: "Deploy trackeid (api, portal) 8f714c1: Commit 1 (+2)",
			contains: []string{
				"• <https://github.com/tr8s/trackeid/commit/196a024c85d9e0f1a2b3c4d5e6f708192a3b4c5d|196a024> Commit 1\\n" +
					"• <https://github.com/tr8s/trackeid/commit/296a024c85d9e0f1a2b3c4d5e6f708192a3b4c5d|296a024> Commit 2\\n" +
					"• <https://github.com/tr8s/trackeid/commit/396a024c85d9e0f1a2b3c4d5e6f708192a3b4c5d|396a024> Commit 3",
				// the compare link stays on the running revision
				"<https://github.com/tr8s/trackeid/compare/5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b...8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910|5f55a51…8f714c1>",
			},
			excludes: []string{subjectLine, "more"},
		},
		{
			name: "more than five commits",
			approval: func() *types.Approval {
				req := groupApproval()
				req.ChangeCount = 8
				req.ChangeCommits = commitList(8)
				return req
			},
			text:     "Deploy trackeid (api, portal) 8f714c1: Commit 1 (+7)",
			contains: []string{"|596a024> Commit 5\\nand 3 more"},
			excludes: []string{"Commit 6"},
		},
		{
			name: "bad or missing commit list keeps the subject",
			approval: func() *types.Approval {
				req := groupApproval()
				req.ChangeCount = 3
				return req
			},
			text:     "Deploy trackeid (api, portal) 8f714c1: Label images with their commit and source",
			contains: []string{subjectLine},
			excludes: []string{"• "},
		},
		{
			name: "unknown host shows plain short shas",
			approval: func() *types.Approval {
				req := groupApproval()
				req.SourceURL = "https://git.example.com/tr8s/trackeid"
				req.ChangeCount = 2
				req.ChangeCommits = commitList(2)
				return req
			},
			contains: []string{"• 196a024 Commit 1\\n• 296a024 Commit 2"},
			excludes: []string{"/commit/196a024"},
		},
		{
			name: "subjects are escaped",
			approval: func() *types.Approval {
				req := groupApproval()
				req.ChangeCount = 2
				req.ChangeCommits = types.ChangeCommits{
					{SHA: "196a024c85d9e0f1a2b3c4d5e6f708192a3b4c5d", Subject: "Render <b> & <@U123>"},
					{SHA: "296a024c85d9e0f1a2b3c4d5e6f708192a3b4c5d", Subject: "Plain"},
				}
				return req
			},
			contains: []string{"|196a024> Render &lt;b&gt; &amp; &lt;@U123&gt;\\n"},
			excludes: []string{"Render <b>", "<@U123>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, text := createCompactBlockMessage(tt.approval())
			rendered := renderBlocks(t, blocks)
			if tt.text != "" && text != tt.text {
				t.Errorf("notification text = %q, want %q", text, tt.text)
			}
			for _, expected := range tt.contains {
				if !strings.Contains(rendered, expected) {
					t.Errorf("expected %q in: %s", expected, rendered)
				}
			}
			for _, unexpected := range tt.excludes {
				if strings.Contains(rendered, unexpected) {
					t.Errorf("did not expect %q in: %s", unexpected, rendered)
				}
			}
		})
	}
}

func TestCompactCommitListWithMigration(t *testing.T) {
	req := groupApproval()
	req.ChangeCount = 3
	req.ChangeCommits = commitList(3)
	req.IncludesMigration = true

	rendered := renderBlocks(t, mustBlocks(createCompactBlockMessage(req)))
	commits := strings.Index(rendered, "Commit 3")
	warning := strings.Index(rendered, migrationWarning)
	if commits == -1 || warning == -1 || warning < commits {
		t.Errorf("expected the migration warning under the commit list, got: %s", rendered)
	}
}
