// Package buildinfo exposes the running binary's version, commit, and build
// time. The control plane uses this (via GET /version and the /readyz body) to
// verify which image a tenant is actually running during fleet rollouts.
package buildinfo

import "runtime/debug"

// Version is the semantic/release version. It defaults to "dev" and can be
// stamped at build time:
//
//	go build -ldflags "-X github.com/calnode/calnode/internal/buildinfo.Version=v1.2.3" ./cmd/calnode
//
// Commit and build time are read automatically from the Go toolchain's embedded
// VCS metadata, so they need no ldflags when built from a git checkout.
var Version = "dev"

// Info is the structured build identity returned by Get.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	Dirty     bool   `json:"dirty,omitempty"`
	GoVersion string `json:"go_version"`
}

// Get assembles the build identity. Commit/BuildTime/Dirty come from the VCS
// stamp the Go toolchain embeds (Go 1.18+); they fall back to "unknown" when the
// binary was built without VCS info (e.g. `go test`, or from an archive).
func Get() Info {
	info := Info{Version: Version, Commit: "unknown", BuildTime: "unknown"}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	info.GoVersion = bi.GoVersion
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.time":
			info.BuildTime = s.Value
		case "vcs.modified":
			info.Dirty = s.Value == "true"
		}
	}
	return info
}
