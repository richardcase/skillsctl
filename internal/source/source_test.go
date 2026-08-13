package source

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Source
		wantErr bool
	}{
		{
			name: "github shorthand",
			raw:  "conorbronsdon/avoid-ai-writing",
			want: Source{
				Channel: ChannelGit,
				RepoURL: "https://github.com/conorbronsdon/avoid-ai-writing.git",
			},
		},
		{
			name: "github shorthand with subpath",
			raw:  "vercel-labs/agent-skills/skills/web-research",
			want: Source{
				Channel: ChannelGit,
				RepoURL: "https://github.com/vercel-labs/agent-skills.git",
				Subpath: "skills/web-research",
			},
		},
		{
			name: "https url",
			raw:  "https://github.com/foo/bar",
			want: Source{Channel: ChannelGit, RepoURL: "https://github.com/foo/bar.git"},
		},
		{
			name: "https url with .git suffix",
			raw:  "https://github.com/foo/bar.git",
			want: Source{Channel: ChannelGit, RepoURL: "https://github.com/foo/bar.git"},
		},
		{
			name: "gitlab url",
			raw:  "https://gitlab.com/foo/bar",
			want: Source{Channel: ChannelGit, RepoURL: "https://gitlab.com/foo/bar.git"},
		},
		{
			name: "ssh url keeps its scp form",
			raw:  "git@github.com:foo/bar.git",
			want: Source{Channel: ChannelGit, RepoURL: "git@github.com:foo/bar.git"},
		},
		{
			name: "ssh url preserves userinfo",
			raw:  "ssh://git@github.com/foo/bar.git",
			want: Source{Channel: ChannelGit, RepoURL: "ssh://git@github.com/foo/bar.git"},
		},
		{
			name: "file url for fixtures",
			raw:  "file:///tmp/fixture/my-skill",
			want: Source{Channel: ChannelGit, RepoURL: "file:///tmp/fixture/my-skill"},
		},
		{
			name: "plugin",
			raw:  "superpowers@claude-plugins-official",
			want: Source{Channel: ChannelPlugin, Plugin: "superpowers", Marketplace: "claude-plugins-official"},
		},
		{
			name: "relative local path",
			raw:  "./my-skill",
			want: Source{Channel: ChannelLocal, Path: "./my-skill"},
		},
		{
			name: "absolute local path",
			raw:  "/srv/skills/my-skill",
			want: Source{Channel: ChannelLocal, Path: "/srv/skills/my-skill"},
		},
		{name: "empty", raw: "", wantErr: true},
		{name: "bare word", raw: "notaskill", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %+v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.raw, err)
			}
			if got.Channel != tc.want.Channel {
				t.Errorf("Channel = %q, want %q", got.Channel, tc.want.Channel)
			}
			if got.RepoURL != tc.want.RepoURL {
				t.Errorf("RepoURL = %q, want %q", got.RepoURL, tc.want.RepoURL)
			}
			if got.Subpath != tc.want.Subpath {
				t.Errorf("Subpath = %q, want %q", got.Subpath, tc.want.Subpath)
			}
			if got.Plugin != tc.want.Plugin {
				t.Errorf("Plugin = %q, want %q", got.Plugin, tc.want.Plugin)
			}
			if got.Marketplace != tc.want.Marketplace {
				t.Errorf("Marketplace = %q, want %q", got.Marketplace, tc.want.Marketplace)
			}
			if got.Path != tc.want.Path {
				t.Errorf("Path = %q, want %q", got.Path, tc.want.Path)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"conorbronsdon/avoid-ai-writing", "github.com/conorbronsdon/avoid-ai-writing"},
		{"vercel-labs/agent-skills/skills/web-research", "github.com/vercel-labs/agent-skills"},
		{"https://gitlab.com/foo/bar", "gitlab.com/foo/bar"},
		{"git@github.com:foo/bar.git", "github.com/foo/bar"},
		{"ssh://git@github.com/foo/bar.git", "github.com/foo/bar"},
		{"file:///tmp/fixture/my-skill", "file/tmp/fixture/my-skill"},
	}
	for _, tc := range tests {
		s, err := Parse(tc.raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.raw, err)
		}
		if got := s.Slug(); got != tc.want {
			t.Errorf("Parse(%q).Slug() = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestSlugNeverEscapes(t *testing.T) {
	raws := []string{
		"file:///a/../../../../etc",
		"file:///../../../etc/passwd",
		"file:///tmp/./fixture/../fixture/my-skill",
		"git@github.com:../../etc",
		"user@github.com:../../../secret",
	}
	for _, raw := range raws {
		s, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		got := s.Slug()
		if got == "" {
			t.Errorf("Parse(%q).Slug() is empty", raw)
		}
		if strings.HasPrefix(got, "/") || strings.Contains(got, "..") {
			t.Errorf("Parse(%q).Slug() = %q, want a safe relative path with no .. segments", raw, got)
		}
	}
}

func TestParseRejectsSourcesWithNoIdentity(t *testing.T) {
	raws := []string{
		"user@..:../..",
		"user@..:..",
	}
	for _, raw := range raws {
		if got, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) = %+v with slug %q, want an error: a source with no usable identity must be rejected", raw, got, got.Slug())
		}
	}
}

func TestParseRejectsTraversingSubpath(t *testing.T) {
	raws := []string{
		"owner/repo/../../../../../../etc",
		"owner/repo/a/../../..",
		"https://github.com/owner/repo/../escape",
	}
	for _, raw := range raws {
		if got, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) = %+v with subpath %q, want an error", raw, got, got.Subpath)
		}
	}
}

func TestParseGitLabSubgroup(t *testing.T) {
	s, err := Parse("https://gitlab.com/group/subgroup/repo.git")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.RepoURL != "https://gitlab.com/group/subgroup/repo.git" {
		t.Errorf("RepoURL = %q, want the full subgroup path", s.RepoURL)
	}
	if s.Subpath != "" {
		t.Errorf("Subpath = %q, want empty: a .git suffix states the repo boundary", s.Subpath)
	}
	if got, want := s.Slug(), "gitlab.com/group/subgroup/repo"; got != want {
		t.Errorf("Slug() = %q, want %q", got, want)
	}
}

func TestParseScpFormKeepsNamespace(t *testing.T) {
	deep, err := Parse("git@gitlab.com:group/subgroup/repo.git")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	shallow, err := Parse("git@gitlab.com:group/repo.git")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if deep.Slug() == shallow.Slug() {
		t.Errorf("distinct repositories share the slug %q", deep.Slug())
	}
}

func TestDefaultName(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"conorbronsdon/avoid-ai-writing", "avoid-ai-writing"},
		{"vercel-labs/agent-skills/skills/web-research", "web-research"},
		{"git@github.com:foo/bar.git", "bar"},
		{"./my-skill", "my-skill"},
		{"superpowers@claude-plugins-official", "superpowers"},
	}
	for _, tc := range tests {
		s, err := Parse(tc.raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.raw, err)
		}
		if got := s.DefaultName(); got != tc.want {
			t.Errorf("Parse(%q).DefaultName() = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
