package writer

import (
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// composeRef is one image reference located in a compose file, tagged with the
// service that declared it so the rewrite can tell the file's own image from
// the ones it merely pulls.
type composeRef struct {
	line       int
	start, end int
	text       string
	service    string
}

// composeLayout is what one pass over a compose file yields: the services and
// the facts that decide which of them the file is about, and every image
// reference with the bytes it occupies.
type composeLayout struct {
	services []manifest.ComposeService
	refs     []composeRef
}

// rewriteCompose edits a compose file line by line: the tag of every image
// matching an edit, and the package's own version into the image the file
// builds. Only the tag inside a scalar is replaced, so indentation, quoting,
// comments and key order all survive.
//
// Two places carry a version. A service's `image:` is the obvious one. A
// service's `build.tags:` is the other: those are the names the built image is
// published under, so they hold the same version the image does.
//
// Which service the version belongs to is not the writer's decision to make
// alone — the reader has to report the version from the same place. Both call
// manifest.ComposeIdentity with the services they found, so the two cannot
// disagree.
func rewriteCompose(path, version string, edits []Edit) (Result, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	var (
		res    Result
		lines  = sp.lines()
		layout = scanComposeLines(lines)
		w      = newImageTagWriter(path, edits)
	)
	identity, _ := manifest.ComposeIdentity(layout.services)
	// A service whose image is the file's own image owns the version, and so
	// does everything it publishes under build.tags. A scaled service naming
	// the same image owns it too, which is why the test is on the repository
	// rather than on the service name.
	owns := make(map[string]bool, len(layout.services))
	for _, s := range layout.services {
		owns[s.Name] = identity != "" && manifest.ParseImageRef(s.Image).Repository == identity
	}
	for _, ref := range layout.refs {
		if owns[ref.service] {
			if err := w.setVersion(ref.line, ref.start, ref.text, version); err != nil {
				return res, err
			}
			continue
		}
		if err := w.match(ref.line, ref.start, ref.text); err != nil {
			return res, err
		}
	}
	changed := w.apply(lines)
	w.fill(&res)
	if changed {
		sp.setLines(lines)
	}
	return res, sp.commit(func(out []byte) error {
		after := scanComposeLines(strings.Split(string(out), "\n"))
		// The file's own images carry the version rather than an edit's range,
		// so checking them against the edits would compare the wrong numbers.
		refs := make([]string, 0, len(after.refs))
		for _, r := range after.refs {
			if !owns[r.service] {
				refs = append(refs, r.text)
			}
		}
		return verifyImageTags(res.Applied, refs)
	})
}

// composeNode is one open mapping key during the walk: the column it sits at,
// and its name.
type composeNode struct {
	indent int
	key    string
}

// scanComposeLines walks a compose file and reports its services and their
// image references.
//
// A compose file nests deeper than the manifests this package's other YAML
// writer handles, and it is full of scalars that look like image references
// without being any: `ports: ["8080:80"]`, `environment: {URL: "redis:6379"}`,
// a `command:` mentioning an image by name. So the walk tracks the exact path
// it is on and reads only two of them —
//
//	services -> <service> -> image
//	services -> <service> -> build -> tags -> <entry>
//
// — and never looks at a byte outside those. Anything else in the file is
// unreachable by construction rather than by a pattern that happens not to
// match it.
func scanComposeLines(lines []string) composeLayout {
	var (
		out   composeLayout
		stack []composeNode
		index = make(map[string]int)
	)
	service := func(name string) int {
		if i, ok := index[name]; ok {
			return i
		}
		index[name] = len(out.services)
		out.services = append(out.services, manifest.ComposeService{Name: name})
		return len(out.services) - 1
	}
	inServices := func(depth int) bool {
		return len(stack) == depth && stack[0].key == composeServices
	}

	for li, raw := range lines {
		line := stripYAMLComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		body := line[indent:]

		// A sequence entry belongs to the block already open, and YAML lets it
		// sit at that block's own column or deeper, so the stack must not be
		// popped for it.
		if body == "-" || strings.HasPrefix(body, "- ") {
			if len(stack) == 4 && stack[0].key == composeServices &&
				stack[2].key == composeBuild && stack[3].key == composeTags {
				if s, e, ok := yamlScalarSpan(line, indent+1); ok {
					out.refs = append(out.refs, composeRef{
						line: li, start: s, end: e, text: line[s:e],
						service: stack[1].key,
					})
				}
			}
			continue
		}

		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		key, valueStart, ok := yamlKey(line)
		if !ok {
			continue
		}
		stack = append(stack, composeNode{indent: indent, key: key})

		switch {
		case inServices(2):
			service(key)
		case inServices(3) && key == composeImage:
			s, e, scalar := yamlScalarSpan(line, valueStart)
			if !scalar {
				continue
			}
			i := service(stack[1].key)
			out.services[i].Image = line[s:e]
			out.refs = append(out.refs, composeRef{
				line: li, start: s, end: e, text: line[s:e],
				service: stack[1].key,
			})
		case inServices(3) && key == composeBuild:
			// Either spelling counts: `build: .` and a nested build block both
			// say this service produces its image here rather than pulling it.
			out.services[service(stack[1].key)].Builds = true
		case len(stack) == 4 && stack[0].key == composeServices &&
			stack[2].key == composeBuild && key == composeTags:
			for _, span := range yamlFlowItems(line, valueStart) {
				out.refs = append(out.refs, composeRef{
					line: li, start: span[0], end: span[1], text: line[span[0]:span[1]],
					service: stack[1].key,
				})
			}
		}
	}
	return out
}

// The compose keys this writer reads. Nothing else in the file is looked at.
const (
	composeServices = "services"
	composeImage    = "image"
	composeBuild    = "build"
	composeTags     = "tags"
)

// yamlFlowItems measures the elements of an inline sequence, `["a:1", "b:2"]`,
// excluding the quotes of a quoted one so a splice preserves the file's
// quoting style. A value that is not a flow sequence yields nothing, which is
// how the block spelling of the same list falls through to the entry handling.
//
// An unterminated sequence yields nothing at all rather than a best guess: the
// line is malformed, and splicing inside a broken literal is how a writer turns
// a typo into a file that no longer parses.
func yamlFlowItems(line string, from int) [][2]int {
	i := from
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || line[i] != '[' {
		return nil
	}
	i++
	var out [][2]int
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t' || line[i] == ',') {
			i++
		}
		if i >= len(line) {
			return nil // ran off the end without a closing bracket
		}
		if line[i] == ']' {
			return out
		}
		if q := line[i]; q == '"' || q == '\'' {
			i++
			start := i
			for i < len(line) && line[i] != q {
				i++
			}
			if i >= len(line) {
				return nil // unterminated quote
			}
			out = append(out, [2]int{start, i})
			i++
			continue
		}
		start := i
		for i < len(line) && line[i] != ',' && line[i] != ']' {
			i++
		}
		end := i
		for end > start && (line[end-1] == ' ' || line[end-1] == '\t') {
			end--
		}
		if end > start {
			out = append(out, [2]int{start, end})
		}
	}
	return nil
}
