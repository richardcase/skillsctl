// Package buildinfo reports the version of the running binary, whether it was
// stamped by GoReleaser or built directly with `go install`.
package buildinfo

import (
	"fmt"
	"runtime/debug"
)

// Set via -ldflags at release time.
var (
	version string
	commit  string
	date    string
)

// Info describes the provenance of the running binary.
type Info struct {
	Version string
	Commit  string
	Date    string
}

func (i Info) String() string {
	return fmt.Sprintf("skillsctl %s (%s, %s)", i.Version, i.Commit, i.Date)
}

// Get returns the build provenance, preferring ldflags-injected values and
// falling back to the module's own build info.
func Get() Info { return get(version, commit, date, debug.ReadBuildInfo) }

func get(version, commit, date string, read func() (*debug.BuildInfo, bool)) Info {
	info := Info{Version: version, Commit: commit, Date: date}

	bi, ok := read()
	if ok {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}

	if info.Version == "" {
		info.Version = "devel"
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.Date == "" {
		info.Date = "unknown"
	}
	return info
}
