package writer

// rewritePodspec edits a CocoaPods podspec: the spec's own `s.version`
// assignment and the version requirement of each `s.dependency` statement
// matching an edit. Every other byte survives verbatim.
//
// The block parameter is matched by shape rather than by name, so a subspec's
// own parameter (`|ss|`) and a platform-scoped declaration
// (`ss.ios.dependency`) are spliced the same way. Only the first `version`
// assignment is the spec's own — a subspec assigning one later is left alone.
// A version assigned from a constant (`s.version = Acme::VERSION`) is not a
// literal and is not replaced with one: the podspec means to compute it.
func rewritePodspec(path, version string, edits []Edit) (Result, error) {
	return rewriteRubyPods(path, edits, func(line string) (int, bool) {
		return rubyDottedCall(line, "dependency")
	}, version)
}
