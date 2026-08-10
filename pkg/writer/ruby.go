package writer

import "strings"

// The CocoaPods manifests are Ruby, so there is no grammar to re-parse a
// rewrite against. Every splice here is confined to the inside of one string
// literal on one line, the replacement is refused outright if it carries a
// byte that could end that literal, and the locator is re-run afterwards to
// confirm it reads what was intended. A statement shape the reader does not
// recognise is reported missing rather than guessed at.

// rubyStripComment cuts a trailing comment, ignoring '#' inside string
// literals, which is also what keeps the '#' of a "#{...}" interpolation from
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

// rubyQuoted measures the first string literal at or after from, returning the
// span its content occupies.
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

// isRubyWritable reports text that can stand inside a string literal without
// changing the file's structure: no quote to close the literal early, no
// backslash to start an escape, no '#' to open an interpolation, no newline.
func isRubyWritable(value string) bool {
	return value != "" && !strings.ContainsAny(value, "'\"\\#\n\r")
}

// rubyBareCall matches a line whose first token is the named method, as in
// `pod 'Alamofire'`, returning the offset where its arguments begin.
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
		return 0, false
	}
	return i, true
}

// rubyDottedCall matches a message sent to a receiver, as in `s.dependency` or
// `ss.ios.dependency`, returning the offset where the arguments begin.
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
// name and the offset just past the '='.
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

// rubyDeclaration reads the name and the sole version requirement of a pod or
// dependency statement whose arguments begin at args.
//
// Only a single requirement is spliceable. A statement carrying several
// (`pod 'X', '>= 1.0', '< 2.0'`) expresses one constraint across two literals
// and replacing either alone would change its meaning; a statement carrying
// none is left as it is rather than gaining one, because a pod with no
// constraint is a deliberate choice in CocoaPods and a git- or path-pinned pod
// has nothing a version could mean.
func rubyDeclaration(line string, args int) (name string, req span, spliceable bool, ok bool) {
	start, end, ok := rubyQuoted(line, args)
	if !ok {
		return "", span{}, false, false
	}
	name = line[start:end]
	if name == "" {
		return "", span{}, false, false
	}

	i := end + 1
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || line[i] != ',' {
		return name, span{}, false, true // no requirement to replace
	}
	start, end, quoted := rubyQuoted(line, i+1)
	if !quoted {
		return name, span{}, false, true // an option hash, not a requirement
	}

	// A second requirement makes the constraint span two literals.
	i = end + 1
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i < len(line) && line[i] == ',' {
		if _, _, more := rubyQuoted(line, i+1); more {
			return name, span{}, false, true
		}
	}
	return name, span{start: int64(start), end: int64(end)}, true, true
}

// isRubyNameByte reports a byte that may appear in a Ruby identifier.
func isRubyNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}
