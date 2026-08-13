package buildinfo

import (
	"runtime/debug"
	"testing"
)

func readerFor(mainVersion string, settings map[string]string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		bi := &debug.BuildInfo{}
		bi.Main.Version = mainVersion
		for k, v := range settings {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: k, Value: v})
		}
		return bi, true
	}
}

func TestGetPrefersLdflags(t *testing.T) {
	got := get("v1.2.3", "abcdef1", "2026-08-13T00:00:00Z", readerFor("v9.9.9", nil))
	if got.Version != "v1.2.3" || got.Commit != "abcdef1" {
		t.Fatalf("ldflags must win, got %+v", got)
	}
}

func TestGetFallsBackToModuleVersion(t *testing.T) {
	got := get("", "", "", readerFor("v0.4.0", map[string]string{
		"vcs.revision": "deadbeef",
		"vcs.time":     "2026-01-02T03:04:05Z",
	}))
	if got.Version != "v0.4.0" {
		t.Errorf("Version = %q, want v0.4.0", got.Version)
	}
	if got.Commit != "deadbeef" {
		t.Errorf("Commit = %q, want deadbeef", got.Commit)
	}
	if got.Date != "2026-01-02T03:04:05Z" {
		t.Errorf("Date = %q, want the vcs.time value", got.Date)
	}
}

func TestGetReportsDevelForUnstampedBuild(t *testing.T) {
	got := get("", "", "", readerFor("(devel)", nil))
	if got.Version != "devel" {
		t.Errorf("Version = %q, want devel", got.Version)
	}
}

func TestGetHandlesMissingBuildInfo(t *testing.T) {
	got := get("", "", "", func() (*debug.BuildInfo, bool) { return nil, false })
	if got.Version != "devel" {
		t.Errorf("Version = %q, want devel", got.Version)
	}
}

func TestStringIsSingleLine(t *testing.T) {
	s := Info{Version: "v1.0.0", Commit: "abc1234", Date: "2026-08-13T00:00:00Z"}.String()
	want := "skillsctl v1.0.0 (abc1234, 2026-08-13T00:00:00Z)"
	if s != want {
		t.Errorf("String() = %q, want %q", s, want)
	}
}
