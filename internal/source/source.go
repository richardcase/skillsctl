// Package source turns a user-supplied source string into a structured Source.
package source

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Channel is the mechanism used to install a skill.
type Channel string

const (
	// ChannelGit represents installation via a git repository.
	ChannelGit Channel = "git"
	// ChannelPlugin represents installation via a plugin marketplace.
	ChannelPlugin Channel = "plugin"
	// ChannelLocal represents installation from a local filesystem path.
	ChannelLocal Channel = "local"
	// ChannelOCI represents installation from an OCI registry.
	ChannelOCI Channel = "oci"
)

// Source is a parsed, canonicalised install source.
type Source struct {
	Channel Channel

	// Git channel.
	RepoURL string // clone URL, passed to git verbatim
	Subpath string // "" when the repository itself is the skill

	// Plugin channel.
	Plugin      string
	Marketplace string

	// Local channel.
	Path string

	// OCI channel.
	Registry   string // registry host[:port]
	Repository string // path within the registry, e.g. "owner/skills"
	Tag        string

	Raw string

	host  string
	owner string
	repo  string
}

var (
	shorthandRe = regexp.MustCompile(`^([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)(?:/(.+))?$`)
	scpRe       = regexp.MustCompile(`^[A-Za-z0-9._-]+@([A-Za-z0-9._-]+):(.+?)(?:\.git)?$`)
	pluginRe    = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9._-]+)$`)
)

// Parse canonicalises raw into a Source, inferring the channel from its shape.
func Parse(raw string) (Source, error) {
	s, err := parse(raw)
	if err != nil {
		return s, err
	}
	if s.Slug() == "" {
		return Source{Raw: raw}, fmt.Errorf("source %q has no usable repository identity", raw)
	}
	// A subpath is joined onto a revision directory at install time, so a
	// . or .. segment would let it escape that directory. There is no
	// legitimate use for one, so this rejects rather than silently stripping
	// it, which would install a different subpath than the one requested.
	for _, seg := range strings.Split(s.Subpath, "/") {
		if seg == "." || seg == ".." {
			return Source{Raw: raw}, fmt.Errorf("source %q has a path segment %q: a subpath may not contain . or .. segments", raw, seg)
		}
		if seg == "" && s.Subpath != "" {
			return Source{Raw: raw}, fmt.Errorf("source %q has an empty path segment: a subpath may not contain //", raw)
		}
	}
	return s, nil
}

// SubpathSep separates a repository from a subpath within it. Inference alone
// cannot express every case: a .git suffix declares the whole path to be the
// repository, and an scp-form URL has no path structure to split at all, so
// without an explicit separator those sources can name no subpath.
const SubpathSep = "//"

// splitSubpath removes an explicit //subpath, returning the source without it.
// The scheme's own // is not a separator, and a trailing one names nothing.
func splitSubpath(raw string) (rest, subpath string) {
	offset := 0
	if i := strings.Index(raw, "://"); i >= 0 {
		offset = i + len("://")
	}

	j := strings.Index(raw[offset:], SubpathSep)
	if j < 0 {
		return raw, ""
	}
	return raw[:offset+j], raw[offset+j+len(SubpathSep):]
}

// parse peels off any explicit //subpath and infers the channel from what is
// left, so every git source form gains the separator at once.
func parse(raw string) (Source, error) {
	repo, subpath := splitSubpath(raw)

	s, err := parseChannel(repo)
	s.Raw = raw
	if err != nil {
		return s, err
	}
	if subpath != "" {
		if s.Channel != ChannelGit {
			return s, fmt.Errorf("source %q: %s names a subpath within a git repository, which the %s channel has no use for",
				raw, SubpathSep, s.Channel)
		}
		// An explicit subpath is a statement, so it wins over the one the
		// shape of the URL implied.
		s.Subpath = subpath
	}
	return s, nil
}

// parseChannel does the actual channel-inferring work. It is split out so Parse
// can apply one validation pass, in one place, to whatever channel this
// produces, rather than repeating a check before every return.
func parseChannel(raw string) (Source, error) {
	s := Source{Raw: raw}

	switch {
	case raw == "":
		return s, fmt.Errorf("empty source")

	case raw == ".", strings.HasPrefix(raw, "./"), strings.HasPrefix(raw, "../"), strings.HasPrefix(raw, "/"), strings.HasPrefix(raw, "~/"):
		s.Channel = ChannelLocal
		s.Path = raw
		return s, nil

	case strings.HasPrefix(raw, "oci://"):
		return parseOCI(raw)

	case strings.Contains(raw, "://"):
		return parseURL(raw)

	case scpRe.MatchString(raw):
		m := scpRe.FindStringSubmatch(raw)
		s.Channel = ChannelGit
		s.RepoURL = raw
		s.host = m[1]
		s.owner, s.repo = splitOwnerRepo(m[2])
		return s, nil

	case pluginRe.MatchString(raw):
		m := pluginRe.FindStringSubmatch(raw)
		s.Channel = ChannelPlugin
		s.Plugin, s.Marketplace = m[1], m[2]
		return s, nil

	case shorthandRe.MatchString(raw):
		m := shorthandRe.FindStringSubmatch(raw)
		s.Channel = ChannelGit
		s.host, s.owner, s.repo = "github.com", m[1], strings.TrimSuffix(m[2], ".git")
		s.RepoURL = fmt.Sprintf("https://github.com/%s/%s.git", s.owner, s.repo)
		s.Subpath = m[3]
		return s, nil
	}

	return s, fmt.Errorf("unrecognised source %q: expected owner/repo, a git URL, plugin@marketplace, or a path", raw)
}

func parseURL(raw string) (Source, error) {
	s := Source{Raw: raw, Channel: ChannelGit}

	u, err := url.Parse(raw)
	if err != nil {
		return s, fmt.Errorf("parse %q: %w", raw, err)
	}

	if u.Scheme == "file" {
		// Fixture and vendored repositories: the whole path identifies the repo.
		s.RepoURL = raw
		s.host = "file"
		s.repo = path.Base(strings.TrimSuffix(u.Path, "/"))
		return s, nil
	}

	// A .git suffix states the repository boundary explicitly, so the whole
	// path is the repo and there is no subpath — this is what makes a GitLab
	// subgroup (group/subgroup/repo.git) installable at all. Without the
	// suffix the split stays ambiguous: parts[0]/parts[1] are owner/repo and
	// anything after is a subpath, which is today's GitHub-shorthand-style
	// behaviour and is left as is.
	trimmedPath := strings.Trim(u.Path, "/")
	explicit := strings.HasSuffix(trimmedPath, ".git")
	trimmed := strings.TrimSuffix(trimmedPath, ".git")

	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return s, fmt.Errorf("git URL %q has no owner/repo path", raw)
	}

	s.host = u.Host
	if explicit {
		s.owner = strings.Join(parts[:len(parts)-1], "/")
		s.repo = parts[len(parts)-1]
	} else {
		s.owner, s.repo = parts[0], parts[1]
		s.Subpath = strings.Join(parts[2:], "/")
	}

	repoURL := url.URL{
		Scheme: u.Scheme,
		User:   u.User,
		Host:   u.Host,
		Path:   fmt.Sprintf("/%s/%s.git", s.owner, s.repo),
	}
	s.RepoURL = repoURL.String()
	return s, nil
}

// parseOCI reads an explicit oci://registry/repository:tag reference. The
// scheme is required rather than inferred from shape, so an OCI source never
// collides with the owner/repo git shorthand.
func parseOCI(raw string) (Source, error) {
	s := Source{Raw: raw, Channel: ChannelOCI}

	rest := strings.TrimPrefix(raw, "oci://")
	repoPart, tag, ok := strings.Cut(rest, ":")
	if !ok || tag == "" {
		return s, fmt.Errorf("oci source %q has no :tag", raw)
	}

	parts := strings.SplitN(repoPart, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return s, fmt.Errorf("oci source %q has no repository path after the registry host", raw)
	}

	s.Registry = parts[0]
	s.Repository = parts[1]
	s.Tag = tag
	return s, nil
}

func splitOwnerRepo(p string) (owner, repo string) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 1 {
		return "", parts[0]
	}
	return strings.Join(parts[:len(parts)-1], "/"), parts[len(parts)-1]
}

// Slug is a stable, filesystem-safe identifier for the repository, used to lay
// out the store. It deliberately excludes the subpath: every skill taken from
// one repository shares one mirror and one revision directory.
func (s Source) Slug() string {
	switch s.Channel {
	case ChannelGit:
		if s.host == "file" {
			u, err := url.Parse(s.RepoURL)
			if err != nil {
				return slugPath("file", s.RepoURL)
			}
			return slugPath("file", u.Path)
		}
		return slugPath(s.host, s.owner, s.repo)
	case ChannelPlugin:
		return slugPath("plugin", s.Marketplace, s.Plugin)
	case ChannelOCI:
		return slugPath("oci", s.Registry, s.Repository)
	default:
		return slugPath("local", s.Path)
	}
}

var unsafeRe = regexp.MustCompile(`[^A-Za-z0-9._/-]`)

// slugPath assembles a slug that is always a safe relative path: no absolute
// prefix, no "." or ".." segments, and no characters that are awkward in a
// filesystem path. Slug's whole job is to be filesystem-safe, so this is
// enforced here rather than trusted at every call site.
func slugPath(parts ...string) string {
	var out []string
	for _, part := range parts {
		for _, seg := range strings.Split(part, "/") {
			switch seg {
			case "", ".", "..":
				continue
			}
			out = append(out, unsafeRe.ReplaceAllString(seg, "-"))
		}
	}
	return path.Join(out...)
}

// DefaultName is the skill name to use when SKILL.md declares none.
func (s Source) DefaultName() string {
	switch s.Channel {
	case ChannelPlugin:
		return s.Plugin
	case ChannelLocal:
		return path.Base(strings.TrimSuffix(s.Path, "/"))
	case ChannelOCI:
		return path.Base(s.Repository)
	default:
		if s.Subpath != "" {
			return path.Base(s.Subpath)
		}
		return s.repo
	}
}
