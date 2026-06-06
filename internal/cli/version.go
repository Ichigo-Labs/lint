package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is the release version, overridden at build time with
// -ldflags "-X github.com/ichigo-labs/lint/internal/cli.version=<v>".
// It is declared in root.go. Commit and build date are not injected here;
// they are read from the VCS stamps Go embeds automatically (see buildInfo).

// buildInfo is the resolved version information for this binary. The release
// version comes from the ldflags-injected `version`; commit/date/dirty come
// from the VCS metadata `go build` records when built inside the repository.
type buildInfo struct {
	Version  string // e.g. "v0.2.0" or "0.1.0-dev"
	Commit   string // VCS revision, or "unknown"
	Date     string // VCS commit time (RFC3339), or "unknown"
	Dirty    bool   // working tree had uncommitted changes at build time
	GoVer    string // e.g. "go1.26.4"
	Platform string // e.g. "linux/amd64"
}

// readBuildInfo resolves version details from the ldflags-injected version and
// Go's embedded build metadata.
func readBuildInfo() buildInfo {
	b := buildInfo{
		Version:  version,
		Commit:   "unknown",
		Date:     "unknown",
		GoVer:    runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Commit = s.Value
		case "vcs.time":
			b.Date = s.Value
		case "vcs.modified":
			b.Dirty = s.Value == "true"
		}
	}
	// With no injected version, prefer the module version the toolchain
	// recorded — but only for builds without local VCS stamps, e.g. one
	// installed via `go install ...@v1.2.3` (yields a clean tag) or `@latest`
	// (yields a commit-bearing pseudo-version). A local `go build` keeps the
	// dev version and shows the commit from the VCS stamps read above, avoiding
	// a redundant "<pseudo-version> (<commit>)".
	if b.Version == defaultVersion && b.Commit == "unknown" &&
		info.Main.Version != "" && info.Main.Version != "(devel)" {
		b.Version = info.Main.Version
	}
	return b
}

// shortCommit trims a full VCS revision to its first 7 characters.
func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

// short returns a one-line version string, e.g. "v0.2.0 (abc1234)".
func (b buildInfo) short() string {
	s := b.Version
	if b.Commit != "unknown" {
		s += " (" + shortCommit(b.Commit)
		if b.Dirty {
			s += "-dirty"
		}
		s += ")"
	}
	return s
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, and build information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			b := readBuildInfo()
			commit := b.Commit
			if b.Dirty && commit != "unknown" {
				commit += " (dirty)"
			}
			fmt.Printf("lint version %s\n", b.Version)
			fmt.Printf("  commit:   %s\n", commit)
			fmt.Printf("  built:    %s\n", b.Date)
			fmt.Printf("  go:       %s\n", b.GoVer)
			fmt.Printf("  platform: %s\n", b.Platform)
		},
	}
}
