package writer

import (
	"fmt"
	"os"
	"strings"
)

// rewritePodfile edits a CocoaPods Podfile line by line: a `pod 'Name', '...'`
// statement whose name matches an edit gets its version requirement replaced,
// and every other byte (the quote style, the surrounding spacing, the trailing
// comment, the option hash) survives verbatim.
//
// A Podfile declares no version of its own, so Rewrite's version argument has
// no target here. Statements the reader cannot splice safely (a pod with no
// requirement, a git- or path-pinned pod, a constraint spread across two
// literals) are reported missing rather than rewritten on a guess.
func rewritePodfile(path string, edits []Edit) (Result, error) {
	return rewriteRubyPods(path, edits, func(line string) (int, bool) {
		return rubyBareCall(line, "pod")
	}, "")
}

// rewriteRubyPods is the splice shared by the Podfile and the podspec: both
// declare dependencies as `<call> 'Name', 'requirement'`, differing only in how
// the call is spelled and whether the file also carries a version of its own.
func rewriteRubyPods(path string, edits []Edit, call func(string) (int, bool), version string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	wanted := make(map[string]int, len(edits))
	for i, e := range edits {
		// CocoaPods has one dependency field: a podspec's `dependency` and a
		// Podfile's `pod` are both plain dependencies, so a kinded edit names
		// something these files cannot express.
		if e.Kind == "" {
			wanted[e.Name] = i
		}
	}

	// Three states, tracked separately. seen means the statement is in the
	// file at all, which is what separates Missing from Skipped. writable
	// means it also carries a version literal to replace. applied means a
	// splice moved it. Indexing all three by edit keeps a pod declared in
	// several targets, an app's and its test target's, from being reported
	// twice.
	var res Result
	seen := make(map[int]bool, len(edits))
	writable := make(map[int]bool, len(edits))
	applied := make(map[int]bool, len(edits))
	lines := strings.Split(string(data), "\n")
	changed := false
	versionDone := version == ""
	for li, raw := range lines {
		line := rubyStripComment(raw)

		if !versionDone {
			if attr, valueStart, ok := rubyAttrAssignment(line); ok && attr == "version" {
				start, end, quoted := rubyQuoted(line, valueStart)
				if quoted {
					versionDone = true // the first assignment is the spec's own
					if current := line[start:end]; current != version {
						if !isRubyWritable(version) {
							return res, fmt.Errorf("%s: refusing to write %q into a Ruby literal", path, version)
						}
						lines[li] = raw[:start] + version + raw[end:]
						res.VersionWritten = true
						changed = true
					}
					continue
				}
			}
		}

		args, ok := call(line)
		if !ok {
			continue
		}
		name, req, spliceable, ok := rubyDeclaration(line, args)
		if !ok {
			continue
		}
		i, want := wanted[name]
		if !want {
			continue
		}
		seen[i] = true
		if !spliceable {
			continue // declared, but nothing here is a version to replace
		}
		writable[i] = true
		if current := line[req.start:req.end]; current == edits[i].Range {
			continue // already the wanted text: no change, not missing
		}
		if !isRubyWritable(edits[i].Range) {
			return res, fmt.Errorf("%s: refusing to write %q into a Ruby literal", path, edits[i].Range)
		}
		applied[i] = true
		lines[li] = raw[:req.start] + edits[i].Range + raw[req.end:]
		changed = true
	}
	// Reported in edit order rather than line order, so the result does not
	// depend on where in the file a declaration happens to sit.
	for i, e := range edits {
		switch {
		case applied[i]:
			res.Applied = append(res.Applied, e)
		case seen[i] && !writable[i]:
			res.Skipped = append(res.Skipped, e)
		case !seen[i]:
			res.Missing = append(res.Missing, e)
		}
	}
	if !changed {
		return res, nil
	}

	// There is no grammar to re-parse against, so the reader is run over the
	// result and must agree that every splice landed where it was aimed.
	out := strings.Join(lines, "\n")
	if err := verifyRubyPods(out, call, res.Applied, version, res.VersionWritten); err != nil {
		return res, fmt.Errorf("%s: internal error: %w", path, err)
	}
	return res, atomicWrite(path, []byte(out))
}

// verifyRubyPods re-reads a rewritten file and checks that every applied edit
// is what the statement now declares.
func verifyRubyPods(out string, call func(string) (int, bool), applied []Edit, version string, versionWritten bool) error {
	want := make(map[string]string, len(applied))
	for _, e := range applied {
		want[e.Name] = e.Range
	}
	seen := make(map[string]bool, len(applied))
	versionSeen := !versionWritten
	for _, raw := range strings.Split(out, "\n") {
		line := rubyStripComment(raw)
		if !versionSeen {
			if attr, valueStart, ok := rubyAttrAssignment(line); ok && attr == "version" {
				start, end, quoted := rubyQuoted(line, valueStart)
				if quoted {
					versionSeen = true
					if line[start:end] != version {
						return fmt.Errorf("rewrite left the version reading %q", line[start:end])
					}
					continue
				}
			}
		}
		args, ok := call(line)
		if !ok {
			continue
		}
		name, req, spliceable, ok := rubyDeclaration(line, args)
		if !ok || !spliceable {
			continue
		}
		text, wanted := want[name]
		if !wanted {
			continue
		}
		if got := line[req.start:req.end]; got != text {
			return fmt.Errorf("rewrite left %s requiring %q, want %q", name, got, text)
		}
		seen[name] = true
	}
	for name := range want {
		if !seen[name] {
			return fmt.Errorf("rewrite lost the declaration of %s", name)
		}
	}
	if !versionSeen {
		return fmt.Errorf("rewrite lost the version assignment")
	}
	return nil
}
