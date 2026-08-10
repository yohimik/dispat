package writer

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
)

// androidRootElement is the only element the writer will splice into.
const androidRootElement = "manifest"

// androidVersionNameAttr is the version attribute, matched on its local name
// so the conventional `android:` prefix is irrelevant.
const androidVersionNameAttr = "versionName"

// rewriteAndroidManifest sets an AndroidManifest.xml's android:versionName by
// replacing only the bytes between that attribute's quotes. The manifest
// declares no dependencies, so every edit is missing by definition, and
// android:versionCode is deliberately left alone: it is a monotonic integer
// rather than a semantic version, and nothing upstream computes one.
//
// A project on a modern Android Gradle Plugin keeps both versions in
// build.gradle and declares neither here; there is then nothing to write, and
// the file is left untouched rather than gaining an attribute the build would
// ignore.
func rewriteAndroidManifest(path, version string, edits []Edit) (Result, error) {
	res := Result{Missing: edits}
	if version == "" {
		return res, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	s, current, ok, err := androidVersionNameSpan(data)
	if err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	if !ok || current == version {
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

	if err := xmlWellFormed(out); err != nil {
		return res, fmt.Errorf("%s: internal error: rewrite produced invalid XML: %w", path, err)
	}
	res.VersionWritten = true
	return res, atomicWrite(path, out)
}

// androidVersionNameSpan locates the byte span of the root <manifest>
// element's version attribute value and decodes its current text. Only the
// root element is considered, and the raw-byte search is confined to that one
// start tag, so the word appearing anywhere else in the document — in a
// comment, in an attribute of some nested element — cannot be hit.
func androidVersionNameSpan(data []byte) (s span, text string, found bool, err error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		prev := dec.InputOffset()
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return span{}, "", false, nil
		}
		if err != nil {
			return span{}, "", false, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != androidRootElement {
			return span{}, "", false, nil // not an Android manifest
		}
		for _, attr := range start.Attr {
			if attr.Name.Local != androidVersionNameAttr {
				continue
			}
			// The window is the whole token, leading whitespace included; the
			// element's own bytes end at the decoder's new position.
			s, ok := attrValueSpan(data[prev:dec.InputOffset()], prev, androidVersionNameAttr)
			if !ok {
				return span{}, "", false, nil
			}
			return s, attr.Value, true, nil
		}
		return span{}, "", false, nil // the root carries no version
	}
}
