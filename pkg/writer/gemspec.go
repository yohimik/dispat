package writer

// gemspecDependencyMethods are the three ways a gemspec declares a dependency.
// The writer does not care which kind each carries — it replaces a version
// wherever it finds the name — so unlike the scanner's table this is just the
// method names.
var gemspecDependencyMethods = []string{
	"add_development_dependency",
	"add_runtime_dependency",
	"add_dependency",
}

// rewriteGemspec edits a RubyGems gemspec: the gem's own `version` assignment
// and the version requirement of each dependency statement matching an edit.
// Every other byte survives verbatim.
//
// The block parameter is matched by shape rather than by name, so whatever
// `Gem::Specification.new do |...|` happens to call it is spliced the same way.
// Nearly every published gem assigns its version from a constant
// (`spec.version = Acme::VERSION`) so its library and its packaging cannot
// disagree; that is not a literal and is deliberately not replaced with one —
// doing so would break the very invariant the constant exists to hold.
func rewriteGemspec(path, version string, edits []Edit) (Result, error) {
	return rewriteRubyPods(path, edits, func(line string) (int, bool) {
		for _, method := range gemspecDependencyMethods {
			if args, ok := rubyDottedCall(line, method); ok {
				return args, true
			}
		}
		return 0, false
	}, version)
}
