package writer

// rewriteGemfile edits a Bundler Gemfile line by line: a `gem 'name', '...'`
// statement whose name matches an edit gets its version requirement replaced,
// and every other byte (the quote style, the surrounding spacing, the trailing
// comment, the option hash, the group blocks) survives verbatim.
//
// A Gemfile declares no version of its own, so Rewrite's version argument has
// no target here. A gem declared in several groups is spliced in all of them
// and reported once. Statements that cannot be spliced safely (a gem with no
// requirement, one pinned to a git revision or a path, a constraint spread
// across two literals) are reported missing rather than rewritten on a guess.
func rewriteGemfile(path string, edits []Edit) (Result, error) {
	return rewriteRubyPods(path, edits, func(line string) (int, bool) {
		return rubyBareCall(line, "gem")
	}, "")
}
