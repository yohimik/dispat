package writer

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
)

// The XML manifests are read through the standard-library decoder with no
// CharsetReader, so a document declaring a legacy single-byte encoding is
// refused rather than rewritten: transcoding would shift every byte offset the
// splice depends on. Both formats this package writes are UTF-8 by convention.

// xmlWellFormed reports whether the whole document still parses, by draining
// the token stream. It is the XML formats' equivalent of the JSON writer's
// json.Valid check: a splice is span-precise, but a manifest is user data and
// no writer here commits bytes it has not proved still parse.
func xmlWellFormed(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// attrValueSpan locates the quoted value of the named attribute inside one
// element's raw bytes: the window is a single start tag, already known to
// carry the attribute, so the scan cannot stray into the rest of the document.
// The returned span covers the bytes between the quotes, offset by base.
//
// The local name is matched on its own, whatever namespace prefix precedes it,
// mirroring how the scanner's struct tags match an attribute regardless of
// prefix.
func attrValueSpan(window []byte, base int64, local string) (span, bool) {
	name := []byte(local)
	for i := 0; i+len(name) <= len(window); i++ {
		j := bytes.Index(window[i:], name)
		if j < 0 {
			return span{}, false
		}
		i += j
		// The character before must end a prefix or separate attributes, so
		// "versionName" never matches inside "myVersionName".
		if i > 0 && !isAttrNameBoundary(window[i-1]) {
			continue
		}
		k := i + len(name)
		for k < len(window) && isXMLSpace(window[k]) {
			k++
		}
		if k >= len(window) || window[k] != '=' {
			continue
		}
		k++
		for k < len(window) && isXMLSpace(window[k]) {
			k++
		}
		if k >= len(window) || (window[k] != '"' && window[k] != '\'') {
			continue
		}
		quote := window[k]
		k++
		end := bytes.IndexByte(window[k:], quote)
		if end < 0 {
			return span{}, false
		}
		return span{start: base + int64(k), end: base + int64(k+end)}, true
	}
	return span{}, false
}

// isAttrNameBoundary reports a byte that may precede an attribute's local
// name: a namespace colon, or the whitespace separating it from the element
// name or the previous attribute.
func isAttrNameBoundary(c byte) bool { return c == ':' || isXMLSpace(c) }

// isXMLSpace reports the whitespace XML allows between attribute tokens.
func isXMLSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
