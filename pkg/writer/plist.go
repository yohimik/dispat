package writer

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// The Info.plist keys and elements the writer touches.
const (
	plistKeyVersion    = "CFBundleShortVersionString"
	plistElementDict   = "dict"
	plistElementKey    = "key"
	plistElementString = "string"
	plistElementRoot   = "plist"
)

// rewritePlist sets an Info.plist's marketing version
// (CFBundleShortVersionString) by replacing only the bytes between its
// <string> tags. A plist declares no dependencies, so every edit is missing by
// definition, and the build counter (CFBundleVersion) is deliberately left
// alone: it is a monotonic integer rather than a semantic version, and nothing
// upstream computes one.
//
// The decoder is the standard-library one with no CharsetReader, so a plist
// declaring a legacy single-byte encoding is refused rather than rewritten:
// transcoding would shift every byte offset the splice depends on. Apple
// writes UTF-8.
func rewritePlist(path, version string, edits []Edit) (Result, error) {
	res := Result{Missing: edits}
	if version == "" {
		return res, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	s, current, ok, err := plistVersionSpan(data)
	if err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	// A missing key, a self-closing value or a build-setting reference all
	// mean there is nothing safe to write. Overwriting $(MARKETING_VERSION)
	// with a literal would silently sever the project's build-setting
	// indirection, which is worse than leaving the file as it is.
	if !ok || isBuildSettingRef(current) || current == version {
		return res, nil
	}

	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(version)); err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	out := make([]byte, 0, len(data)+escaped.Len())
	out = append(out, data[:s.start]...)
	out = append(out, escaped.Bytes()...)
	out = append(out, data[s.end:]...)

	// The splice is span-precise, but a manifest is user data: never write
	// bytes back without proving they still parse.
	if err := xmlWellFormed(out); err != nil {
		return res, fmt.Errorf("%s: internal error: rewrite produced invalid XML: %w", path, err)
	}
	res.VersionWritten = true
	return res, atomicWrite(path, out)
}

// isBuildSettingRef reports an Xcode build-setting reference — $(NAME) or
// ${NAME} — rather than a literal value. It mirrors the scanner's rule.
func isBuildSettingRef(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasPrefix(v, "$(") && strings.HasSuffix(v, ")") ||
		strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}")
}

// plistVersionSpan locates the byte span of the marketing version's <string>
// content and decodes its current text. Only the root dictionary is searched:
// a real Info.plist nests dictionaries and arrays carrying <key> elements of
// their own, and splicing one of those would rewrite the wrong value.
func plistVersionSpan(data []byte) (s span, text string, found bool, err error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	inDict, err := plistSeekRootDict(dec)
	if err != nil || !inDict {
		return span{}, "", false, err
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return span{}, "", false, nil
		}
		if err != nil {
			return span{}, "", false, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			if _, closed := tok.(xml.EndElement); closed {
				return span{}, "", false, nil // the root dictionary closed
			}
			continue
		}
		if start.Name.Local != plistElementKey {
			if err := dec.Skip(); err != nil {
				return span{}, "", false, err
			}
			continue
		}
		var key string
		if err := dec.DecodeElement(&key, &start); err != nil {
			return span{}, "", false, err
		}
		wanted := strings.TrimSpace(key) == plistKeyVersion
		value, err := plistNextValue(dec, data)
		if err != nil {
			return span{}, "", false, err
		}
		if !value.open {
			return span{}, "", false, nil // the dictionary closed on a dangling key
		}
		if wanted && value.spliceable {
			return value.span, value.text, true, nil
		}
	}
}

// plistValue is one value element read after a <key>: whether the enclosing
// dictionary is still open, and, for a <string> with real content bytes
// behind it, the span that content occupies.
type plistValue struct {
	open       bool
	spliceable bool
	span       span
	text       string
}

// plistSeekRootDict advances the decoder past the root dictionary's opening
// tag, accepting both the conventional <plist> wrapper and a bare <dict>
// document. A well-formed plist that holds no dictionary reports false.
func plistSeekRootDict(dec *xml.Decoder) (bool, error) {
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case plistElementDict:
			return true, nil
		case plistElementRoot:
			// Descend into the wrapper and look for its dictionary child.
		default:
			return false, nil
		}
	}
}

// plistNextValue consumes the element following a <key>. Only a <string> is
// spliceable; every other value type is skipped whole, so a nested container
// never becomes a splice target.
func plistNextValue(dec *xml.Decoder, data []byte) (plistValue, error) {
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return plistValue{}, nil
		}
		if err != nil {
			return plistValue{}, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return plistValue{}, nil
		case xml.StartElement:
			if t.Name.Local != plistElementString {
				if err := dec.Skip(); err != nil {
					return plistValue{}, err
				}
				return plistValue{open: true}, nil
			}
			return plistStringValue(dec, data)
		}
	}
}

// plistStringValue measures the content of the <string> element the decoder
// has just entered. A self-closing <string/> has no content to splice into —
// writing after the tag would land the version outside the element — so the
// value counts as spliceable only when the bytes its content ends at really
// are a closing tag.
func plistStringValue(dec *xml.Decoder, data []byte) (plistValue, error) {
	start := dec.InputOffset()
	var b strings.Builder
	for {
		prev := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return plistValue{}, err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			if !bytes.HasPrefix(data[prev:], []byte("</")) {
				return plistValue{open: true}, nil
			}
			return plistValue{
				open:       true,
				spliceable: true,
				span:       span{start: start, end: prev},
				text:       b.String(),
			}, nil
		}
	}
}
