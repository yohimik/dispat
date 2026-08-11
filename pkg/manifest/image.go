package manifest

import "strings"

// ImageRef is one Docker image reference split into the parts a manifest
// declares. A reference packs up to four things into one string —
// registry, repository path, tag and digest — and the reader and the writer
// must agree byte for byte on where each one starts, so the split lives here
// rather than in either half.
//
// The parts are kept exactly as written. No registry is inferred, no "latest"
// is filled in and "library/" is never prepended: a manifest declares what it
// declares, and a writer that normalised the text would rewrite lines nobody
// asked it to touch.
type ImageRef struct {
	// Repository is everything before the tag: the registry, its optional
	// port, and the path. "redis", "ghcr.io/acme/api", "localhost:5000/api".
	Repository string
	// Tag is the declared tag, empty when the reference names none.
	Tag string
	// Digest is the declared digest ("sha256:..."), empty when the reference
	// pins none.
	Digest string
	// TagStart and TagEnd are the byte offsets of Tag inside the reference the
	// split was given, so a writer can splice the tag without rebuilding the
	// text around it. Both are -1 when there is no tag.
	TagStart, TagEnd int
}

// ParseImageRef splits an image reference.
//
// Two separators do the work, and the order matters. The digest is cut at the
// first "@", because everything after it belongs to the digest. The tag is
// then the text after a ":" that comes *after the last* "/" — that one rule is
// what tells the port in "localhost:5000/api" apart from the tag in
// "redis:7.2", since a registry's port can only appear before the first slash
// and a tag only after the last one.
//
// A reference is never rejected. Anything unrecognisable comes back as a bare
// repository with no tag, which is inert: nothing matches it and no writer can
// splice it.
func ParseImageRef(ref string) ImageRef {
	out := ImageRef{TagStart: -1, TagEnd: -1}
	rest := ref
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		out.Digest = rest[i+1:]
		rest = rest[:i]
	}
	out.Repository = rest
	i := strings.LastIndexByte(rest, ':')
	if i < 0 || i < strings.LastIndexByte(rest, '/') {
		return out
	}
	tag := rest[i+1:]
	if tag == "" {
		// A trailing colon declares nothing. Reporting the repository without a
		// tag span leaves the reference matchable but unwritable, which is the
		// honest reading of a half-written line.
		out.Repository = rest[:i]
		return out
	}
	out.Repository, out.Tag = rest[:i], tag
	out.TagStart, out.TagEnd = i+1, len(rest)
	return out
}

// HasTag reports a reference carrying a tag a writer could splice.
func (r ImageRef) HasTag() bool { return r.TagStart >= 0 }

// Pinned reports a reference carrying a digest. The digest is what actually
// gets pulled, so the tag beside it is a label rather than a selector and
// rewriting it would leave the file claiming a version it does not use.
func (r ImageRef) Pinned() bool { return r.Digest != "" }

// Interpolated reports a reference whose repository or tag defers to a build
// argument or an environment variable ("${BASE}:${TAG}", "$IMAGE"). The value
// is resolved outside the file, and writing a literal over it would sever the
// indirection it exists for.
func (r ImageRef) Interpolated() bool {
	return strings.ContainsRune(r.Repository, '$') || strings.ContainsRune(r.Tag, '$')
}

// maxTagLength is the tag limit the registry specification sets.
const maxTagLength = 128

// ValidTag reports text a registry would accept as a tag: up to 128 characters
// of letters, digits, underscores, periods and dashes, not opening with a
// separator. A writer checks it before splicing so a version that cannot be a
// tag is refused outright instead of producing a file that no longer builds.
func ValidTag(tag string) bool {
	if tag == "" || len(tag) > maxTagLength {
		return false
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		case c == '.' || c == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
