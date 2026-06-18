// Package meta holds build-wide constants the CLI reports (version, issue
// tracker). In the Node build these come from package.json; here they are
// compile-time constants kept in sync with it.
package meta

const (
	// Version is the cappu version (mirrors package.json "version").
	Version = "1.0.0"
	// IssueTracker is the bug tracker `cappu rage` opens (package.json bugs.url).
	IssueTracker = "https://github.com/nikeee/cappu/issues"
)
