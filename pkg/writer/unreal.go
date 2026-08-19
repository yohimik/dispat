package writer

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The descriptor fields the writer touches.
const (
	unrealKeyVersionName = "VersionName"
	unrealKeyVersion     = "Version"
	unrealKeyPlugins     = "Plugins"
	unrealKeyName        = "Name"
)

// rewriteUProject reports what a .uproject can and cannot be asked to do. The
// descriptor declares no version of its own, and its plugins carry no version
// text at all, so there is never anything to write; what the function is for
// is answering honestly which edits named something the file declares.
func rewriteUProject(path string, edits []Edit) (Result, error) {
	plugins, err := unrealPluginNames(path)
	if err != nil {
		return Result{}, err
	}
	return unrealPartition(edits, plugins), nil
}

// rewriteUPlugin sets a .uplugin's VersionName by replacing only the bytes of
// that one string. Version beside it is the build counter and is left alone:
// SetBuild is where a counter moves.
//
// A plugin the descriptor lists is reported skipped rather than missing.
// Unreal resolves a plugin by name against the project and the engine, so the
// declaration genuinely carries no version text to write; the dependency is
// there, and calling it missing would report a disagreement that does not
// exist. Every healthy Unreal descriptor is in this state permanently, which
// is exactly what Skipped is for.
func rewriteUPlugin(path, version string, edits []Edit) (Result, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	versionSpan, _, plugins, err := unrealSpans(sp.bytes())
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	res := unrealPartition(edits, plugins)
	if version == "" || versionSpan == nil {
		return res, nil
	}
	if string(current(sp.bytes(), *versionSpan)) == version {
		return res, nil
	}
	sp.replace(*versionSpan, quote(version))
	res.VersionWritten = true
	return res, sp.commit(verifyJSON)
}

// setUPluginBuild writes a .uplugin's Version, the monotonic counter Unreal
// orders plugin builds by. It is a bare integer in every descriptor the engine
// writes, so a quoted value here would produce a file the build tool refuses;
// the splice puts back the shape it found, quoting only where the file already
// quoted. The key is never created.
func setUPluginBuild(path, build string) (Result, error) {
	var res Result
	if !allDigits(build) {
		return res, errNotAnInteger(path, unrealKeyVersion, build)
	}
	sp, err := openSplicer(path)
	if err != nil {
		return res, err
	}
	_, buildSpan, _, err := unrealSpans(sp.bytes())
	if err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	if buildSpan == nil {
		return res, nil
	}
	raw := sp.at(*buildSpan)
	if string(current(sp.bytes(), *buildSpan)) == build {
		return res, nil
	}
	if len(raw) > 0 && raw[0] == '"' {
		sp.replace(*buildSpan, quote(build))
	} else {
		sp.replace(*buildSpan, []byte(build))
	}
	res.BuildWritten = true
	return res, sp.commit(verifyJSON)
}

// unrealPartition sorts edits into the two buckets a descriptor can honestly
// report. A plugin the file lists is declared but unwritable, so it is
// skipped; anything else the caller named is missing. An edit carrying a
// dependency kind the format has no field for is missing too, on the same
// terms composer reports a peer dependency: the format cannot express it.
func unrealPartition(edits []Edit, plugins map[string]bool) Result {
	var res Result
	for _, e := range edits {
		if e.Kind == manifest.KindDependencies && plugins[e.Name] {
			res.Skipped = append(res.Skipped, e)
			continue
		}
		res.Missing = append(res.Missing, e)
	}
	return res
}

// unrealPluginNames reads just the declared plugin names of a descriptor.
func unrealPluginNames(path string) (map[string]bool, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return nil, err
	}
	_, _, plugins, err := unrealSpans(sp.bytes())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return plugins, nil
}

// unrealSpans locates, in one pass over the token stream, the value spans of
// the descriptor's VersionName and Version and the set of names its Plugins
// array declares. A field the descriptor does not carry comes back nil, which
// is how a .uproject reports having no version to write.
func unrealSpans(data []byte) (versionName, version *span, plugins map[string]bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, nil, err
	}
	if tok != json.Delim('{') {
		return nil, nil, nil, fmt.Errorf("top level is not an object")
	}
	plugins = make(map[string]bool)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, nil, nil, err
		}
		key, _ := keyTok.(string)
		afterKey := dec.InputOffset()
		switch key {
		case unrealKeyVersionName:
			s, err := scalarSpan(data, dec, afterKey)
			if err != nil {
				return nil, nil, nil, err
			}
			versionName = &s
		case unrealKeyVersion:
			s, err := scalarSpan(data, dec, afterKey)
			if err != nil {
				return nil, nil, nil, err
			}
			version = &s
		case unrealKeyPlugins:
			open, err := dec.Token()
			if err != nil {
				return nil, nil, nil, err
			}
			if open != json.Delim('[') {
				return nil, nil, nil, fmt.Errorf("%q is not an array", key)
			}
			for dec.More() {
				if err := unrealPluginEntry(dec, plugins); err != nil {
					return nil, nil, nil, err
				}
			}
			if _, err := dec.Token(); err != nil { // closing ']'
				return nil, nil, nil, err
			}
		default:
			if err := skipValue(dec); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	return versionName, version, plugins, nil
}

// unrealPluginEntry consumes one element of a Plugins array and records the
// name it declares. An element that is not an object, or an object with no
// Name, declares nothing resolvable and is stepped over rather than refused:
// one odd entry must not cost the descriptor its other declarations.
func unrealPluginEntry(dec *json.Decoder, into map[string]bool) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil // a scalar element, already consumed
	}
	if d != '{' {
		return skipOpened(dec)
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		if key, _ := keyTok.(string); key == unrealKeyName {
			valueTok, err := dec.Token()
			if err != nil {
				return err
			}
			if name, ok := valueTok.(string); ok && name != "" {
				into[name] = true
			}
			continue
		}
		if err := skipValue(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token() // closing '}'
	return err
}

// skipOpened consumes the rest of a composite whose opening delimiter has
// already been read.
func skipOpened(dec *json.Decoder) error {
	for depth := 1; depth > 0; {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}
