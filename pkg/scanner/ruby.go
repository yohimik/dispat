package scanner

import "strings"

// The CocoaPods manifests are Ruby, and a Podfile in the wild really does run
// arbitrary code: shelling out, branching on the environment, defining methods
// that targets call. Parsing that properly means a Ruby interpreter, so this
// package does the opposite — it recognises the handful of statement shapes
// that declare dependencies and ignores everything else. A line it does not
// understand contributes nothing rather than a guess.

// rubyStripComment cuts a trailing comment, ignoring '#' inside string
// literals — which is also what keeps the '#' of a "#{...}" interpolation from
// truncating the line. It only ever truncates, so offsets into the result stay
// valid in the original.
func rubyStripComment(line string) string {
	single, double := false, false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case single:
			if c == '\\' {
				i++
			} else if c == '\'' {
				single = false
			}
		case double:
			if c == '\\' {
				i++
			} else if c == '"' {
				double = false
			}
		case c == '\'':
			single = true
		case c == '"':
			double = true
		case c == '#':
			return line[:i]
		}
	}
	return line
}

// rubyQuoted measures the first string literal at or after from, returning
// the span its content occupies. Both Ruby spellings count; the caller decides
// whether the content is usable, since a double-quoted literal may interpolate.
func rubyQuoted(line string, from int) (start, end int, ok bool) {
	i := from
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || (line[i] != '\'' && line[i] != '"') {
		return 0, 0, false
	}
	quote := line[i]
	i++
	start = i
	for i < len(line) {
		if line[i] == '\\' {
			i += 2
			continue
		}
		if line[i] == quote {
			return start, i, true
		}
		i++
	}
	return 0, 0, false
}

// isRubyLiteral reports a string whose text is what it says: no "#{...}"
// interpolation to resolve. An interpolated value names a variable this
// package cannot evaluate — a path like "#{@prefix_path}/Libraries" resolves
// to nothing on disk, and recording it would send ResolveLocalDir chasing a
// folder that does not exist.
func isRubyLiteral(value string) bool { return !strings.Contains(value, "#{") }

// rubyBareCall matches a line whose first token is the named method, as in
// `pod 'Alamofire'` or `target 'App' do`, returning the offset where its
// arguments begin.
func rubyBareCall(line, method string) (int, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if !strings.HasPrefix(line[i:], method) {
		return 0, false
	}
	i += len(method)
	if i < len(line) && isRubyNameByte(line[i]) {
		return 0, false // a longer identifier that merely starts with it
	}
	return i, true
}

// rubyDottedCall matches a message sent to a receiver, as in `s.dependency` or
// the platform-scoped `ss.ios.dependency`, returning the offset where the
// arguments begin. The receiver chain is whatever the podspec's block
// parameter happens to be called, so it is matched by shape rather than by
// name.
func rubyDottedCall(line, method string) (int, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	chain, next, ok := rubyIdentChain(line, i)
	if !ok || len(chain) < 2 || chain[len(chain)-1] != method {
		return 0, false
	}
	return next, true
}

// rubyAttrAssignment matches `receiver.attr = value`, returning the attribute
// name and the offset just past the '='. A comparison or a hash rocket is not
// an assignment, and neither is a bare local variable: the receiver chain must
// have at least one dot, which is what distinguishes `s.version = '1.0'` from
// anything else on the line.
func rubyAttrAssignment(line string) (attr string, valueStart int, ok bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	chain, next, ok := rubyIdentChain(line, i)
	if !ok || len(chain) < 2 {
		return "", 0, false
	}
	for next < len(line) && (line[next] == ' ' || line[next] == '\t') {
		next++
	}
	if next >= len(line) || line[next] != '=' {
		return "", 0, false
	}
	next++
	// "==", "=>" and "=~" are not assignments.
	if next < len(line) && (line[next] == '=' || line[next] == '>' || line[next] == '~') {
		return "", 0, false
	}
	return chain[len(chain)-1], next, true
}

// rubyIdentChain reads a dotted identifier chain starting at i.
func rubyIdentChain(line string, i int) (chain []string, next int, ok bool) {
	for {
		start := i
		for i < len(line) && isRubyNameByte(line[i]) {
			i++
		}
		if i == start {
			return nil, 0, false
		}
		chain = append(chain, line[start:i])
		if i >= len(line) || line[i] != '.' {
			return chain, i, true
		}
		i++
	}
}

// rubyOption reads a hash option in either spelling — `:key => 'value'` and
// the newer `key: 'value'` — returning the span of its string literal.
func rubyOption(line, key string) (start, end int, ok bool) {
	for _, form := range []struct{ token, sep string }{{":" + key, "=>"}, {key + ":", ""}} {
		i := strings.Index(line, form.token)
		if i < 0 {
			continue
		}
		// The token must start a word, so ":path" never matches ":subpath".
		if i > 0 && (isRubyNameByte(line[i-1]) || line[i-1] == ':') {
			continue
		}
		j := i + len(form.token)
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		if form.sep != "" {
			if !strings.HasPrefix(line[j:], form.sep) {
				continue
			}
			j += len(form.sep)
		}
		if start, end, ok := rubyQuoted(line, j); ok {
			return start, end, true
		}
	}
	return 0, 0, false
}

// isRubyNameByte reports a byte that may appear in a Ruby identifier.
func isRubyNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}
