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
	return s, nil
}

// parse does the actual channel-inferring work of Parse. It is split out so
// Parse can apply one validation pass, in one place, to whatever channel this
// produces, rather than repeating a check before every return.
func parse(raw string) (Source, error) {
	s := Source{Raw: raw}

	switch {
	case raw == "":
		return s, fmt.Errorf("empty source")

	case raw == ".", strings.HasPrefix(raw, "./"), strings.HasPrefix(raw, "../"), strings.HasPrefix(raw, "/"), strings.HasPrefix(raw, "~/"):
		s.Channel = ChannelLocal
		s.Path = raw
		return s, nil

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

	trimmed := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return s, fmt.Errorf("git URL %q has no owner/repo path", raw)
	}

	s.host = u.Host
	s.owner, s.repo = parts[0], parts[1]
	s.Subpath = strings.Join(parts[2:], "/")

	repoURL := url.URL{
		Scheme: u.Scheme,
		User:   u.User,
		Host:   u.Host,
		Path:   fmt.Sprintf("/%s/%s.git", s.owner, s.repo),
	}
	s.RepoURL = repoURL.String()
	return s, nil
}

func splitOwnerRepo(p string) (owner, repo string) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[len(parts)-1]
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
	default:
		if s.Subpath != "" {
			return path.Base(s.Subpath)
		}
		return s.repo
	}
}
