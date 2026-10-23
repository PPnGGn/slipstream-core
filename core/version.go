package core

// buildCommit is the commit the core was built from. The Makefile sets it at
// link time (-ldflags "-X slipstream-core/core.buildCommit=<commit>", with
// "-dirty" after the commit when the working tree had uncommitted changes);
// a build that does not (go test, go build) leaves "unknown".
var buildCommit = "unknown"

// BuildCommit returns the commit of the core this binary was built from, or
// "unknown". xray's version says which xray is inside; this says which core.
func BuildCommit() string { return buildCommit }
