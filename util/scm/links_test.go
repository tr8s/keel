package scm

import "testing"

func TestChangeLinks(t *testing.T) {
	const (
		oldRev = "5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b"
		newRev = "62c200e9a1b2c3d4e5f60718293a4b5c6d7e8f90"
	)

	tests := []struct {
		name   string
		source string
		oldRev string
		newRev string
		want   Links
	}{
		{
			name:   "github",
			source: "https://github.com/tr8s/trackeid",
			oldRev: oldRev,
			newRev: newRev,
			want: Links{
				Commit:  "https://github.com/tr8s/trackeid/commit/" + newRev,
				Compare: "https://github.com/tr8s/trackeid/compare/" + oldRev + "..." + newRev,
			},
		},
		{
			name:   "github deep link is trimmed to the repository",
			source: "https://github.com/tr8s/trackeid/tree/main/",
			newRev: newRev,
			want:   Links{Commit: "https://github.com/tr8s/trackeid/commit/" + newRev},
		},
		{
			name:   "bitbucket",
			source: "https://bitbucket.org/selfleaders/valuestree",
			oldRev: oldRev,
			newRev: newRev,
			want: Links{
				Commit:  "https://bitbucket.org/selfleaders/valuestree/commits/" + newRev,
				Compare: "https://bitbucket.org/selfleaders/valuestree/branches/compare/" + newRev + "%0D" + oldRev,
			},
		},
		{
			name:   "gitlab with subgroup",
			source: "https://gitlab.com/group/subgroup/project",
			oldRev: oldRev,
			newRev: newRev,
			want: Links{
				Commit:  "https://gitlab.com/group/subgroup/project/-/commit/" + newRev,
				Compare: "https://gitlab.com/group/subgroup/project/-/compare/" + oldRev + "..." + newRev,
			},
		},
		{
			name:   "self-hosted gitlab",
			source: "https://gitlab.example.com/team/app/-/tree/main",
			newRev: newRev,
			want:   Links{Commit: "https://gitlab.example.com/team/app/-/commit/" + newRev},
		},
		{
			name:   "trailing .git",
			source: "https://github.com/tr8s/trackeid.git",
			oldRev: oldRev,
			newRev: newRev,
			want: Links{
				Commit:  "https://github.com/tr8s/trackeid/commit/" + newRev,
				Compare: "https://github.com/tr8s/trackeid/compare/" + oldRev + "..." + newRev,
			},
		},
		{
			name:   "unknown host",
			source: "https://git.example.com/tr8s/trackeid",
			oldRev: oldRev,
			newRev: newRev,
			want:   Links{},
		},
		{
			name:   "missing old revision",
			source: "https://github.com/tr8s/trackeid",
			newRev: newRev,
			want:   Links{Commit: "https://github.com/tr8s/trackeid/commit/" + newRev},
		},
		{
			name:   "same revision has no compare link",
			source: "https://github.com/tr8s/trackeid",
			oldRev: newRev,
			newRev: newRev,
			want:   Links{Commit: "https://github.com/tr8s/trackeid/commit/" + newRev},
		},
		{
			name:   "missing new revision",
			source: "https://github.com/tr8s/trackeid",
			oldRev: oldRev,
			want:   Links{},
		},
		{
			name:   "not an http url",
			source: "git@github.com:tr8s/trackeid.git",
			newRev: newRev,
			want:   Links{},
		},
		{
			name:   "missing repository path",
			source: "https://github.com/tr8s",
			newRev: newRev,
			want:   Links{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChangeLinks(tt.source, tt.oldRev, tt.newRev); got != tt.want {
				t.Errorf("ChangeLinks(%q, %q, %q) = %+v, want %+v", tt.source, tt.oldRev, tt.newRev, got, tt.want)
			}
		})
	}
}

func TestNormalizeSource(t *testing.T) {
	for source, want := range map[string]string{
		"https://github.com/tr8s/trackeid":       "https://github.com/tr8s/trackeid",
		"https://github.com/tr8s/trackeid.git":   "https://github.com/tr8s/trackeid",
		" https://github.com/tr8s/trackeid.git/": "https://github.com/tr8s/trackeid",
	} {
		if got := NormalizeSource(source); got != want {
			t.Errorf("NormalizeSource(%q) = %q, want %q", source, got, want)
		}
	}
}
