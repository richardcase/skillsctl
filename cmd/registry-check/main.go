// Command registry-check validates registry/skills.json against the network
// and reports candidate skills seen in heilcheng/awesome-agent-skills but not
// yet in the registry, for the scheduled registry-refresh workflow to turn
// into a pull request. By default it never modifies registry/skills.json.
// With --fix it writes a proposed fix — broken entries dropped, new
// candidates appended as stub entries — but that write only ever lands on a
// PR branch for a human to review: resolving a candidate name to a real
// owner/repo stays a human decision, since several of that list's entries
// are monorepo subpaths a name alone cannot disambiguate.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/registry"
	"github.com/richardcase/skillsctl/internal/source"
)

// awesomeListReadmeURL is the curation source new candidates are read from.
const awesomeListReadmeURL = "https://raw.githubusercontent.com/heilcheng/awesome-agent-skills/main/README.md"

func main() {
	registryPath := flag.String("registry", "registry/skills.json", "path to the registry file to check")
	fix := flag.Bool("fix", false, "write broken-entry removals and new-candidate stubs back to the registry file")
	flag.Parse()

	report, err := run(context.Background(), *registryPath, gitx.New(), http.DefaultClient, awesomeListReadmeURL, *fix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry-check: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(report)
}

// run loads registryPath, validates every entry's source against the
// network via g, finds candidates in the README at readmeURL not already
// present, and renders both as a Markdown report. When fix is true, it also
// writes registryPath: broken entries removed, new candidates appended as
// stub entries for a human to complete.
func run(ctx context.Context, registryPath string, g gitx.Git, client *http.Client, readmeURL string, fix bool) (string, error) {
	entries, err := registry.Load(registryPath)
	if err != nil {
		return "", fmt.Errorf("load %s: %w", registryPath, err)
	}

	broken := validate(ctx, g, entries)
	candidates, err := newCandidates(ctx, client, readmeURL, entries)
	if err != nil {
		return "", fmt.Errorf("find new candidates: %w", err)
	}

	if fix {
		if err := registry.Save(registryPath, fixedEntries(entries, broken, candidates)); err != nil {
			return "", fmt.Errorf("write %s: %w", registryPath, err)
		}
	}

	return report(broken, candidates), nil
}

// stubDescription flags a candidate entry as machine-generated and
// incomplete: registry-check only ever scrapes an owner/repo name from the
// awesome-list, never a description, tags, agents, or confirmation that the
// source isn't actually a subpath within a monorepo.
const stubDescription = "TODO: added automatically by registry-refresh — verify the source " +
	"(it may need a /subpath), then fill in description, tags and agents before merging."

// fixedEntries returns entries with every broken one dropped and one stub
// entry appended per candidate (capped at maxReportedCandidates, the same
// bound the report applies, so one run can't balloon the file). Order among
// the surviving original entries is preserved; stub entries are appended in
// the same sorted order newCandidates already returns.
func fixedEntries(entries []registry.Entry, broken []brokenEntry, candidates []string) []registry.Entry {
	isBroken := make(map[string]bool, len(broken))
	for _, b := range broken {
		isBroken[b.Name+"\x00"+b.Source] = true
	}

	fixed := make([]registry.Entry, 0, len(entries))
	for _, e := range entries {
		if isBroken[e.Name+"\x00"+e.Source] {
			continue
		}
		fixed = append(fixed, e)
	}

	if len(candidates) > maxReportedCandidates {
		candidates = candidates[:maxReportedCandidates]
	}
	for _, c := range candidates {
		_, name, _ := strings.Cut(c, "/")
		fixed = append(fixed, registry.Entry{
			Name:        name,
			Source:      c,
			Description: stubDescription,
		})
	}
	return fixed
}

// brokenEntry is a registry entry whose source no longer resolves.
type brokenEntry struct {
	Name   string
	Source string
	Reason string
}

// validate resolves every git-channel entry's source against the network,
// returning the ones that no longer do. A plugin or local source names
// nothing this tool can check over the network, so it is skipped rather than
// flagged.
func validate(ctx context.Context, g gitx.Git, entries []registry.Entry) []brokenEntry {
	var broken []brokenEntry
	for _, e := range entries {
		src, err := source.Parse(e.Source)
		if err != nil {
			broken = append(broken, brokenEntry{Name: e.Name, Source: e.Source, Reason: err.Error()})
			continue
		}
		if src.Channel != source.ChannelGit {
			continue
		}
		if _, err := g.Resolve(ctx, src.RepoURL, ""); err != nil {
			broken = append(broken, brokenEntry{Name: e.Name, Source: e.Source, Reason: err.Error()})
		}
	}
	return broken
}

// awesomeListLinkRe matches heilcheng/awesome-agent-skills' README link
// shape specifically — [owner/name](https://agent-skill.co/... — rather than
// any markdown link, so an unrelated link elsewhere in the README (an
// official directory, a screenshot) is never mistaken for a skill entry.
var awesomeListLinkRe = regexp.MustCompile(`\[([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)\]\(https://agent-skill\.co/`)

// newCandidates fetches the awesome-list README at readmeURL and returns the
// linked skill names not yet present in entries, sorted and deduplicated.
func newCandidates(ctx context.Context, client *http.Client, readmeURL string, entries []registry.Entry) ([]string, error) {
	body, err := fetchReadme(ctx, client, readmeURL)
	if err != nil {
		return nil, err
	}

	// known is keyed on owner/name, not just the trailing skill name: two
	// different owners can publish a skill with the same name, and the
	// registry's owner is only a "known" match for that exact owner. The
	// owner is the first path segment of Entry.Source, which is always a
	// source.Parse-shaped "owner/repo[/subpath]" string.
	known := make(map[string]bool, len(entries))
	for _, e := range entries {
		owner, _, ok := strings.Cut(e.Source, "/")
		if !ok {
			continue
		}
		known[strings.ToLower(owner+"/"+e.Name)] = true
	}

	seen := make(map[string]bool)
	var out []string
	for _, m := range awesomeListLinkRe.FindAllStringSubmatch(body, -1) {
		fullName := m[1]
		// Check registry membership using the full owner/name: a bare
		// skill-name match would treat a different owner's same-named skill
		// as already known and drop it from the report.
		if known[strings.ToLower(fullName)] {
			continue
		}
		// Deduplicate using the full owner/name to preserve distinct candidates
		// from different owners that share a trailing skill name
		seenKey := strings.ToLower(fullName)
		if seen[seenKey] {
			continue
		}
		seen[seenKey] = true
		out = append(out, fullName)
	}
	sort.Strings(out)
	return out, nil
}

func fetchReadme(ctx context.Context, client *http.Client, readmeURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readmeURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", readmeURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: unexpected status %s", readmeURL, resp.Status)
	}
	blob, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", readmeURL, err)
	}
	return string(blob), nil
}

// maxReportedCandidates caps how many new candidates report lists, so a
// first run against an empty registry (or a long dry spell) doesn't dump
// the awesome-list's hundreds of entries into one issue body. broken
// entries stay uncapped: that list should stay small, and every one of
// them matters.
const maxReportedCandidates = 50

// report renders broken entries and new candidates as Markdown for the
// tracking issue, or "" when there is nothing to flag.
func report(broken []brokenEntry, candidates []string) string {
	if len(broken) == 0 && len(candidates) == 0 {
		return ""
	}

	var b strings.Builder
	if len(broken) > 0 {
		fmt.Fprintf(&b, "## Broken entries (%d)\n\n", len(broken))
		for _, e := range broken {
			fmt.Fprintf(&b, "- **%s** (`%s`): %s\n", e.Name, e.Source, e.Reason)
		}
		b.WriteString("\n")
	}
	if len(candidates) > 0 {
		fmt.Fprintf(&b, "## New candidates (%d)\n\n", len(candidates))
		b.WriteString("Seen in [heilcheng/awesome-agent-skills](https://github.com/heilcheng/awesome-agent-skills), " +
			"not yet in `registry/skills.json`. Each needs a human to resolve its real " +
			"`owner/repo[/subpath]` before it can be added.\n\n")
		shown := candidates
		truncated := 0
		if len(shown) > maxReportedCandidates {
			truncated = len(shown) - maxReportedCandidates
			shown = shown[:maxReportedCandidates]
		}
		for _, c := range shown {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		if truncated > 0 {
			fmt.Fprintf(&b, "- …and %d more\n", truncated)
		}
	}
	return b.String()
}
