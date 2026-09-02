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

// Commit is the git SHA, stamped the same way when the build has no VCS metadata
// to read. The Docker build is exactly that case: the image is built from a copied
// source tree with no .git, so the toolchain's automatic stamp is empty and every
// container reported commit "unknown".
//
// That was tolerable while only tagged releases were deployed, since the version
// identified the build. It stopped being tolerable once branch images could be
// deployed too: those report version "dev", so without this there was nothing in a
// running instance that said which commit it was.
//
//	-ldflags "-X github.com/calnode/calnode/internal/buildinfo.Commit=$(git rev-parse HEAD)"
var Commit = ""

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
	// An explicit stamp wins: where both exist they agree, and where only one does
	// it is this one (the container build has no VCS metadata to read).
	if Commit != "" {
		info.Commit = Commit
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	info.GoVersion = bi.GoVersion
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if Commit == "" { // an explicit ldflags stamp wins; see Commit above
				info.Commit = s.Value
			}
		case "vcs.time":
			info.BuildTime = s.Value
		case "vcs.modified":
			info.Dirty = s.Value == "true"
		}
	}
	return info
}
