// Package version holds build metadata, overridable with -ldflags.
package version

var (
	// Version is the semantic version of this build.
	Version = "0.1.0-dev"
	// Commit is the git commit this build was made from.
	Commit = "unknown"
	// Milestone is the development milestone this build implements.
	Milestone = 2
)
