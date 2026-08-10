package main

import (
	"runtime/debug"
	"testing"
)

// moduleBuild is the build info Go records for `go install <pkg>@<version>`.
// The module proxy serves a zip rather than a checkout, so no VCS stamp exists.
func moduleBuild(version string) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Version: version}}
}

// checkoutBuild is the build info Go records for `go build` where it can read the
// repository. Go derives the version from the VCS and appends "+dirty" to it
// itself when the tree has uncommitted changes.
func checkoutBuild(version, revision, when string, dirty bool) *debug.BuildInfo {
	modified := "false"
	if dirty {
		modified = "true"
	}
	return &debug.BuildInfo{
		Main: debug.Module{Version: version},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.time", Value: when},
			{Key: "vcs.modified", Value: modified},
		},
	}
}

// TestResolveBuildIdentity_ReportsTheBuildEachInstallRouteProduces: a binary
// names the build it came from however it was installed.
// Given the linker values and build info each install route leaves behind,
// when resolveBuildIdentity reads them, then it reports the version, commit,
// and date that route recorded.
// Why it matters: a bug report is only actionable when the reporter can say
// which build produced it, and `go install` is the route the README leads with.
func TestResolveBuildIdentity_ReportsTheBuildEachInstallRouteProduces(t *testing.T) {
	// Given: the values the linker leaves when no -X flag is passed
	unset := buildIdentity{version: "dev", commit: "none", date: "unknown"}
	const (
		revision = "9f7f0ccd189f20dafe739b39bbd7c5dc9ce29386"
		when     = "2026-08-10T05:50:53Z"
		pseudo   = "v0.0.0-20260810055053-9f7f0ccd189f"
	)

	tests := []struct {
		name   string
		linker buildIdentity
		info   *debug.BuildInfo
		want   buildIdentity
	}{
		{
			// GoReleaser writes dist/ into the tree as it builds, so Go stamps
			// the version dirty while the linker is passing the clean tag. The
			// linker has to win, or every release reports itself as modified.
			name:   "a goreleaser release keeps what the linker baked in",
			linker: buildIdentity{version: "v0.1.0", commit: revision, date: when},
			info:   checkoutBuild("v0.1.0+dirty", "0000000000000000000000000000000000000000", "2020-01-01T00:00:00Z", true),
			want:   buildIdentity{version: "v0.1.0", commit: revision, date: when},
		},
		{
			name:   "go install at a tag reports the tag",
			linker: unset,
			info:   moduleBuild("v0.1.0"),
			want:   buildIdentity{version: "v0.1.0", commit: "none", date: "unknown"},
		},
		{
			name:   "go build in a checkout reports the commit it was built from",
			linker: unset,
			info:   checkoutBuild(pseudo, revision, when, false),
			want:   buildIdentity{version: pseudo, commit: revision, date: when},
		},
		{
			name:   "go build over uncommitted changes keeps the marker Go appends",
			linker: unset,
			info:   checkoutBuild(pseudo+"+dirty", revision, when, true),
			want:   buildIdentity{version: pseudo + "+dirty", commit: revision, date: when},
		},
		{
			name:   "a build Go could not trace to a repository keeps the defaults",
			linker: unset,
			info:   moduleBuild("(devel)"),
			want:   unset,
		},
		{
			name:   "a binary stripped of build info keeps the defaults",
			linker: unset,
			info:   nil,
			want:   unset,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the identity is resolved from that route's evidence
			got := resolveBuildIdentity(tt.linker, tt.info)

			// Then: it names the build that route produced
			if got != tt.want {
				t.Errorf("resolveBuildIdentity() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
