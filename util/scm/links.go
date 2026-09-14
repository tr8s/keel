// Package scm builds links to the source code change behind an image, based on
// the org.opencontainers.image.source and org.opencontainers.image.revision
// image labels.
package scm

import (
	"net/url"
	"strings"
)

// Links points at a code change. An empty URL means it could not be built,
// for example because the source is hosted on an unrecognized service.
type Links struct {
	// Commit links to the new revision
	Commit string
	// Compare links to the changes between the old and the new revision
	Compare string
}

// NormalizeSource trims whitespace, trailing slashes and a trailing ".git"
// from a source repository URL so that equivalent sources compare equal.
func NormalizeSource(source string) string {
	source = strings.TrimSuffix(strings.TrimSpace(source), "/")
	return strings.TrimSuffix(source, ".git")
}

// ChangeLinks builds links to newRevision in the source repository and, when
// oldRevision is known and differs, to the changes between the two revisions.
// GitHub, Bitbucket Cloud and GitLab (gitlab.com or a gitlab.* host) are
// supported; other sources yield empty links.
func ChangeLinks(source, oldRevision, newRevision string) Links {
	if newRevision == "" {
		return Links{}
	}

	u, err := url.Parse(NormalizeSource(source))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return Links{}
	}

	var segments []string
	for _, segment := range strings.Split(u.Path, "/") {
		if segment != "" {
			segments = append(segments, url.PathEscape(segment))
		}
	}
	if len(segments) < 2 {
		return Links{}
	}

	base := u.Scheme + "://" + u.Host
	newRev := url.PathEscape(newRevision)
	oldRev := url.PathEscape(oldRevision)
	compare := oldRevision != "" && oldRevision != newRevision

	var links Links
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "github.com":
		repo := base + "/" + segments[0] + "/" + segments[1]
		links.Commit = repo + "/commit/" + newRev
		if compare {
			links.Compare = repo + "/compare/" + oldRev + "..." + newRev
		}
	case host == "bitbucket.org":
		repo := base + "/" + segments[0] + "/" + segments[1]
		links.Commit = repo + "/commits/" + newRev
		if compare {
			links.Compare = repo + "/branches/compare/" + newRev + "%0D" + oldRev
		}
	case host == "gitlab.com" || strings.HasPrefix(host, "gitlab."):
		// GitLab projects can be nested in subgroups; the project path ends
		// where GitLab's "-" route separator starts
		project := segments
		for i, segment := range segments {
			if segment == "-" {
				project = segments[:i]
				break
			}
		}
		if len(project) < 2 {
			return Links{}
		}
		repo := base + "/" + strings.Join(project, "/")
		links.Commit = repo + "/-/commit/" + newRev
		if compare {
			links.Compare = repo + "/-/compare/" + oldRev + "..." + newRev
		}
	}

	return links
}
