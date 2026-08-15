package scanner

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// The Apple bundle-metadata keys the scanner reads out of an Info.plist.
const (
	plistKeyIdentifier   = "CFBundleIdentifier"
	plistKeyVersion      = "CFBundleShortVersionString"
	plistKeyBuildNumber  = "CFBundleVersion"
	plistElementDict     = "dict"
	plistElementKey      = "key"
	plistElementString   = "string"
	plistRootElementName = "plist"
)

// parsePlist reads an Info.plist: the bundle identifier as the package name,
// the marketing version (CFBundleShortVersionString) as the version, and the
// build counter (CFBundleVersion) as the build number. A plist declares no
// dependencies, so this is an identity-only manifest, it feeds versioning, not
// the dependency graph.
//
// Xcode routinely writes these values as build-setting references
// ($(MARKETING_VERSION), $(PRODUCT_BUNDLE_IDENTIFIER)). A referenced version
// is kept verbatim, matching how the Maven parser keeps a ${property}; a
// referenced *identifier* is dropped instead, because every app in a
// repository spells it identically and NameIndex would report the shared
// literal as an ambiguous name rather than an unresolved one.
func parsePlist(rel string, data []byte) (Manifest, error) {
	fields, err := plistTopLevelStrings(data)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Path:        rel,
		Ecosystem:   EcosystemPlist,
		Version:     fields[plistKeyVersion],
		BuildNumber: fields[plistKeyBuildNumber],
		Root:        isRoot(rel),
	}
	if id := fields[plistKeyIdentifier]; !isBuildSettingRef(id) {
		m.Name = id
	}
	return m, nil
}

// isBuildSettingRef reports an Xcode build-setting reference ($(NAME) or
// ${NAME}) rather than a literal value.
func isBuildSettingRef(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasPrefix(v, "$(") && strings.HasSuffix(v, ")") ||
		strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}")
}

// plistTopLevelStrings collects the <string> values of the root dictionary's
// keys. Only the top level counts: a real Info.plist nests dictionaries and
// arrays (CFBundleURLTypes, UIApplicationSceneManifest) that carry <key>
// elements of their own, and a flat walk would happily read a CFBundleVersion
// out of one of them. Nested values are skipped wholesale. A plist with no
// root dictionary returns a nil map with a nil error, deliberately: the nil
// map reads as absent keys at the call site, which is the honest answer.
func plistTopLevelStrings(data []byte) (map[string]string, error) {
	dec := newXMLDecoder(data)
	found, err := plistSeekRootDict(dec)
	if err != nil || !found {
		return nil, err
	}
	fields := make(map[string]string)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return fields, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			// The root dictionary's own closing tag ends the walk.
			if _, closed := tok.(xml.EndElement); closed {
				return fields, nil
			}
			continue
		}
		if start.Name.Local != plistElementKey {
			// A value with no key before it: malformed, but one odd entry must
			// not void the manifest.
			if err := dec.Skip(); err != nil {
				return nil, err
			}
			continue
		}
		var key string
		if err := dec.DecodeElement(&key, &start); err != nil {
			return nil, err
		}
		value, ok, err := plistNextValue(dec)
		if err != nil {
			return nil, err
		}
		if !ok {
			return fields, nil // the dictionary closed on a dangling key
		}
		fields[strings.TrimSpace(key)] = value
	}
}

// plistSeekRootDict advances the decoder past the opening tag of the root
// dictionary. The wrapper element is <plist>, but a bare <dict> document is
// accepted too; either way the dictionary sought is the outermost one. A
// well-formed document that simply holds no dictionary (a plist whose root is
// an array) reports found=false rather than an error: it declares nothing,
// which is a valid answer, not a malformed file.
func plistSeekRootDict(dec *xml.Decoder) (found bool, err error) {
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
		case plistRootElementName:
			// Descend into the wrapper and look for its dictionary child.
		default:
			return false, nil
		}
	}
}

// plistNextValue consumes the element following a <key> and returns its text
// when it is a <string>. Every other value type (dictionaries, arrays,
// integers, booleans) is skipped whole and reported as an empty string, so a
// nested container never leaks its own keys into the walk. ok is false once
// the enclosing dictionary closes.
func plistNextValue(dec *xml.Decoder) (value string, ok bool, err error) {
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return "", false, nil
		case xml.StartElement:
			if t.Name.Local != plistElementString {
				if err := dec.Skip(); err != nil {
					return "", false, err
				}
				return "", true, nil
			}
			var text string
			if err := dec.DecodeElement(&text, &t); err != nil {
				return "", false, err
			}
			return text, true, nil
		}
	}
}
