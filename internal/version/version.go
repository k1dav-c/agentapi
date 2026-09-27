// package version defines the current version of agentapi.

package version

// Version is the agentapi version, without a leading "v". Release builds
// override it with the release tag via
// -ldflags "-X github.com/coder/agentapi/internal/version.Version=<version>"
// (see the Makefile and .github/workflows/release.yml). Builds without the
// override report a development version.
var Version = "0.0.0-dev"
