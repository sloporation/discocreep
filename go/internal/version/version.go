// Package version is the release this binary was built from. Release builds
// set it with -ldflags "-X gitlab.com/jacxb/bots/bxt/go/internal/version.Version=1.2.3";
// anything else (go run, local docker builds) reports "dev".
package version

// Version is the release tag, or "dev".
var Version = "dev"
