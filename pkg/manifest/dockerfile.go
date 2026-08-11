package manifest

import "strings"

// DockerRef is one image reference located inside a Dockerfile: which line
// holds it and which bytes of that line it occupies.
//
// The offsets are what let the writer splice a tag without rebuilding the line
// around it, and they are why locating references lives here rather than in
// each half separately. The reader only needs to know *which* images a file
// depends on and the writer only needs to know *where* they are, but both must
// agree on the answer to the first question — a reference the reader counts as
// a dependency and the writer cannot find would silently never be reconciled.
type DockerRef struct {
	Line       int
	Start, End int
	Text       string
}

// scratchImage is the empty base every Dockerfile may name. It is a keyword
// rather than an image: nothing publishes it and nothing can depend on it.
const scratchImage = "scratch"

// DockerfileRefs finds every image reference a Dockerfile depends on.
//
// Three instructions name an image. FROM names the base of a stage. COPY
// --from and RUN --mount=…,from= pull files out of one. Each may instead name
// an earlier stage, by its AS alias or by its position, and a stage is part of
// this file rather than something it depends on, so those are filtered out as
// they are met — along with `scratch`, which is a keyword.
//
// Instructions may run across several physical lines through a trailing
// backslash, and a splice needs a physical line, so the walk tracks the
// instruction it is inside rather than joining the lines up first. Stage
// aliases come into scope as they are defined, which means an alias only
// shadows an image for the instructions below it. That is the scope the
// builder gives them, and the reason `FROM tools:1.0 AS first` above
// `FROM alpine:3 AS tools` still reports a dependency on tools:1.0.
func DockerfileRefs(lines []string) []DockerRef {
	var (
		out      []DockerRef
		stages   = make(map[string]bool)
		keyword  string
		args     int
		newAlias string
	)
	for li, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			// Blank lines and whole-line comments carry no tokens, inside a
			// continuation or outside one. Skipping them without touching the
			// state keeps a comment in the middle of a continued RUN from
			// looking like the end of it. A parser directive (# syntax=…) is a
			// comment and goes the same way.
			continue
		}
		body, continues := dockerLineBody(line)
		fields := dockerFields(line, body)
		if keyword == "" {
			if len(fields) == 0 {
				continue
			}
			keyword, args = strings.ToLower(fields[0].text), 0
			fields = fields[1:]
		}
		for _, f := range fields {
			switch keyword {
			case "from":
				switch {
				case strings.HasPrefix(f.text, "--"):
					// A flag: --platform and its kin never name the base.
				case args == 0:
					args++
					if !stages[strings.ToLower(f.text)] && f.text != scratchImage {
						out = append(out, DockerRef{Line: li, Start: f.start, End: f.end, Text: f.text})
					}
				case args == 1 && strings.EqualFold(f.text, "as"):
					args++
				default:
					newAlias = f.text
					args++
				}
			case "copy", "run":
				// A flag only belongs to the instruction while it precedes the
				// instruction's own arguments. Past the first of those,
				// "--from=" is a flag of the command being run —
				// `RUN mytool sync --from=a/b:1.0` names a tool's option, not
				// an image — and rewriting it would corrupt a shell line.
				if args > 0 || !strings.HasPrefix(f.text, "--") {
					args++
					continue
				}
				for _, r := range dockerFromFlag(f) {
					if stages[strings.ToLower(r.Text)] || r.Text == scratchImage || isStageIndex(r.Text) {
						continue
					}
					r.Line = li
					out = append(out, r)
				}
			}
		}
		if continues {
			continue
		}
		// The instruction ends here, so its alias comes into scope now: a stage
		// cannot name itself as its own base.
		if keyword == "from" && newAlias != "" {
			stages[strings.ToLower(newAlias)] = true
		}
		keyword, args, newAlias = "", 0, ""
	}
	return out
}

// dockerLineBody reports how much of a physical line is instruction text, and
// whether the instruction carries on into the next one. The trailing backslash
// and the whitespace around it are not part of any token.
func dockerLineBody(line string) (body int, continues bool) {
	end := len(line)
	for end > 0 && isDockerSpace(line[end-1]) {
		end--
	}
	if end > 0 && line[end-1] == '\\' {
		return end - 1, true
	}
	return end, false
}

// dockerField is one whitespace-delimited token with the bytes it occupies.
type dockerField struct {
	text       string
	start, end int
}

// isDockerSpace reports the bytes that separate a Dockerfile's tokens. The
// carriage return is one of them, so a CRLF file tokenises exactly like an LF
// one instead of gluing a "\r" onto the last token of every line.
func isDockerSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' }

// dockerFields splits the first limit bytes of a line into tokens.
func dockerFields(line string, limit int) []dockerField {
	var out []dockerField
	for i := 0; i < limit; {
		for i < limit && isDockerSpace(line[i]) {
			i++
		}
		start := i
		for i < limit && !isDockerSpace(line[i]) {
			i++
		}
		if i > start {
			out = append(out, dockerField{text: line[start:i], start: start, end: i})
		}
	}
	return out
}

// dockerFromFlag reads the image references one COPY or RUN flag carries:
// --from= names one directly, --mount= names one inside its comma-separated
// options. The offsets are carried through the nesting, so a reference buried
// in a mount option still points at its own bytes in the line.
func dockerFromFlag(f dockerField) []DockerRef {
	if v, ok := strings.CutPrefix(f.text, "--from="); ok {
		if v == "" {
			return nil
		}
		at := f.start + len("--from=")
		return []DockerRef{{Start: at, End: at + len(v), Text: v}}
	}
	v, ok := strings.CutPrefix(f.text, "--mount=")
	if !ok {
		return nil
	}
	var (
		out  []DockerRef
		base = f.start + len("--mount=")
		at   = 0
	)
	for _, opt := range strings.Split(v, ",") {
		if src, ok := strings.CutPrefix(opt, "from="); ok && src != "" {
			start := base + at + len("from=")
			out = append(out, DockerRef{Start: start, End: start + len(src), Text: src})
		}
		at += len(opt) + 1 // the comma the split consumed
	}
	return out
}

// isStageIndex reports a --from naming a stage by position ("--from=0"). It
// points inside this file, exactly as an alias does.
func isStageIndex(ref string) bool {
	for i := 0; i < len(ref); i++ {
		if ref[i] < '0' || ref[i] > '9' {
			return false
		}
	}
	return ref != ""
}
