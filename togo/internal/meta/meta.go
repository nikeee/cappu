// Package meta holds build-wide constants the CLI reports (version, issue
// tracker). In the Node build these come from package.json.
package meta

// Version is the cappu version reported by `cappu --version`. Release binaries
// stamp it from the git tag via `-ldflags "-X .../meta.Version=<tag>"` (see
// togo/Makefile + .github/workflows/CD.yaml), so the shipped binary always
// reports its own build version. The default below is the dev-build fallback,
// kept in step with package.json "version" by the npm version hook.
var Version = "0.1.15"

// IssueTracker is the bug tracker `cappu rage` opens (package.json bugs.url).
const IssueTracker = "https://github.com/nikeee/cappu/issues"
