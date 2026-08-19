package writer

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The build-counter writers. The version rewriters deliberately never touch a
// build counter (a monotonic count is not a semantic version, and the two
// move for different reasons), so writing one is its own entry point rather
// than a flag on Rewrite. The formats are the ones whose counter the
// scanner reads: CFBundleVersion, android:versionCode,
// CURRENT_PROJECT_VERSION, Gradle's versionCode, the + suffix a pubspec
// version carries, Unity's AndroidBundleVersionCode and per-platform
// buildNumber counters, version/code in a Godot export preset, a .uplugin's
// Version, and the Android StoreVersion in Config/DefaultEngine.ini.

// ErrNoBuildCounter marks a manifest whose format carries no build counter;
// test with errors.Is.
var ErrNoBuildCounter = errors.New("writer: this format carries no build counter")

// buildWriters maps each format with a build counter onto its writer.
var buildWriters = map[manifest.Format]func(path, build string) (Result, error){
	manifest.FormatPlist:           setPlistBuild,
	manifest.FormatAndroidManifest: setAndroidBuild,
	manifest.FormatXcodeProject:    setXcodeBuild,
	manifest.FormatGradleBuild:     setGradleBuild,
	manifest.FormatPubspec:         setPubspecBuild,

	manifest.FormatUnityProjectSettings: setUnityBuild,
	manifest.FormatGodotExportPresets:   setGodotExportBuild,
	manifest.FormatUnrealPlugin:         setUPluginBuild,
	manifest.FormatUnrealEngineConfig:   setUnrealEngineBuild,
}

// SetBuild writes the build counter of the manifest at path, in whatever
// place its format keeps one. The counter's declaration is never created,
// only updated (a pubspec's + suffix is the exception, appended to the
// version it annotates): a project that does not track a counter has decided
// so, and a CI stamp should not overrule that.
//
// A file name no manifest format claims gives ErrUnsupportedManifest; a
// recognised format without a counter gives ErrNoBuildCounter. A counter that
// is absent from the file, deferred to a build setting, or already reading
// the wanted value leaves the file alone with BuildWritten false.
func SetBuild(path, build string) (Result, error) {
	if build == "" {
		return Result{}, fmt.Errorf("%s: writer: no build value to write", path)
	}
	format, ok := manifest.FormatOfPath(path)
	if !ok {
		return Result{}, fmt.Errorf("%s: %w", path, ErrUnsupportedManifest)
	}
	set, ok := buildWriters[format]
	if !ok {
		return Result{}, fmt.Errorf("%s: %w", path, ErrNoBuildCounter)
	}
	res, err := set(path, build)
	res.Path = path
	return res, err
}

// plistKeyBuild is the bundle's build counter.
const plistKeyBuild = "CFBundleVersion"

// setPlistBuild writes CFBundleVersion between its <string> tags, the same
// splice rewritePlist applies to the marketing version.
func setPlistBuild(path, build string) (Result, error) {
	var res Result
	sp, err := openSplicer(path)
	if err != nil {
		return res, err
	}
	s, current, ok, err := plistStringSpan(sp.bytes(), plistKeyBuild)
	if err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	// A missing key, a self-closing value or a build-setting reference all
	// mean there is nothing safe to write; $(CURRENT_PROJECT_VERSION) is an
	// indirection to keep, not a value to overwrite.
	if !ok || isDeferredValue(current) || current == build {
		return res, nil
	}
	sp.replace(s, xmlEscape(build))
	res.BuildWritten = true
	return res, sp.commit(verifyXML)
}

// androidVersionCodeAttr is the manifest's integer build counter.
const androidVersionCodeAttr = "versionCode"

// setAndroidBuild writes android:versionCode between its quotes. Android
// requires an integer, so anything else is refused before the file is
// touched: writing a word here would produce a manifest no build accepts.
func setAndroidBuild(path, build string) (Result, error) {
	var res Result
	if !allDigits(build) {
		return res, errNotAnInteger(path, "versionCode", build)
	}
	sp, err := openSplicer(path)
	if err != nil {
		return res, err
	}
	s, current, ok, err := androidAttrSpan(sp.bytes(), androidVersionCodeAttr)
	if err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	if !ok || current == build {
		return res, nil
	}
	sp.replace(s, xmlEscape(build))
	res.BuildWritten = true
	return res, sp.commit(verifyXML)
}

// pbxKeyBuild is the project's build counter, kept per build configuration
// like the marketing version.
const pbxKeyBuild = "CURRENT_PROJECT_VERSION"

// setXcodeBuild writes CURRENT_PROJECT_VERSION in every build configuration
// that declares it, under the same three guards rewriteXcodeProj stands its
// marketing-version writes on.
func setXcodeBuild(path, build string) (Result, error) {
	var res Result
	if strings.ContainsAny(build, "\";{}\n\r") {
		return res, fmt.Errorf("%s: refusing to write %q into a project file: it could not survive as one token", path, build)
	}
	sp, err := openSplicer(path)
	if err != nil {
		return res, err
	}
	lines := sp.lines()
	before := 0
	changed := false
	for i, line := range lines {
		key, value, span, ok := pbxSetting(line)
		if !ok || key != pbxKeyBuild {
			continue
		}
		before++
		if value == build {
			continue
		}
		lines[i] = line[:span[0]] + build + line[span[1]:]
		changed = true
	}
	if !changed {
		return res, nil
	}
	sp.setLines(lines)
	res.BuildWritten = true
	return res, sp.commit(func(out []byte) error {
		return pbxVerify(sp.bytes(), out, pbxKeyBuild, build, before)
	})
}

// setGradleBuild writes the versionCode inside defaultConfig, in either
// dialect's spelling. Gradle requires an integer, so anything else is refused
// the way the Android manifest writer refuses it.
func setGradleBuild(path, build string) (Result, error) {
	var res Result
	if !allDigits(build) {
		return res, errNotAnInteger(path, "versionCode", build)
	}
	sp, err := openSplicer(path)
	if err != nil {
		return res, err
	}
	var (
		scope     []string
		inComment bool
		found     bool
		lines     = sp.lines()
		changed   bool
	)
	for li, raw := range lines {
		var masked string
		masked, inComment = gradleMask(raw, inComment)
		if !found && gradleScopeHas(scope, "defaultConfig") {
			if start, end, ok := gradleNumberSpan(masked, "versionCode"); ok {
				found = true
				if raw[start:end] != build {
					lines[li] = raw[:start] + build + raw[end:]
					changed = true
				}
			}
		}
		scope = gradleUpdateScope(scope, raw, masked)
	}
	if !changed {
		return res, nil
	}
	sp.setLines(lines)
	res.BuildWritten = true
	return res, sp.commit(func(out []byte) error {
		var (
			scope     []string
			inComment bool
		)
		for _, raw := range strings.Split(string(out), "\n") {
			var masked string
			masked, inComment = gradleMask(raw, inComment)
			if gradleScopeHas(scope, "defaultConfig") {
				if start, end, ok := gradleNumberSpan(masked, "versionCode"); ok {
					if raw[start:end] != build {
						return fmt.Errorf("rewrite left versionCode reading %q", raw[start:end])
					}
					return nil
				}
			}
			scope = gradleUpdateScope(scope, raw, masked)
		}
		return fmt.Errorf("rewrite lost the versionCode assignment")
	})
}

// gradleNumberSpan measures the integer literal a property assigns, in either
// dialect's spelling: `versionCode 42` and `versionCode = 42`.
func gradleNumberSpan(masked, name string) (start, end int, ok bool) {
	i := gradleSkipSpace(masked, 0)
	if !strings.HasPrefix(masked[i:], name) {
		return 0, 0, false
	}
	i += len(name)
	if i < len(masked) && isGradleNameByte(masked[i]) {
		return 0, 0, false
	}
	i = gradleSkipSpace(masked, i)
	if i < len(masked) && masked[i] == '=' {
		i = gradleSkipSpace(masked, i+1)
	}
	start = i
	for i < len(masked) && masked[i] >= '0' && masked[i] <= '9' {
		i++
	}
	if i == start {
		return 0, 0, false
	}
	return start, i, true
}

// setPubspecBuild writes the + suffix of a pubspec's version, which is how
// pub spells a build counter (`version: 1.2.3+4`). A version without one
// gains it; this is the one counter that lives inside the version scalar
// rather than in a field of its own, so appending does not create a
// declaration the project never made.
func setPubspecBuild(path, build string) (Result, error) {
	var res Result
	sp, err := openSplicer(path)
	if err != nil {
		return res, err
	}
	lines := sp.lines()
	for i, raw := range lines {
		line := stripYAMLComment(raw)
		if strings.TrimSpace(line) == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, valueStart, ok := yamlKey(line)
		if !ok || key != "version" {
			continue
		}
		start, end, ok := yamlScalarSpan(line, valueStart)
		if !ok {
			return res, nil // `version:` with nothing after it: nothing to annotate
		}
		version := line[start:end]
		if plus := strings.IndexByte(version, '+'); plus >= 0 {
			if version[plus+1:] == build {
				return res, nil
			}
			lines[i] = raw[:start+plus+1] + build + raw[end:]
		} else {
			lines[i] = raw[:end] + "+" + build + raw[end:]
		}
		sp.setLines(lines)
		res.BuildWritten = true
		return res, sp.commit(nil)
	}
	return res, nil
}

// errNotAnInteger is the refusal every integer counter shares. Several
// platforms parse their build counter as a number and reject a package whose
// counter is a word, so the value is checked before the file is opened rather
// than written and discovered at upload time.
func errNotAnInteger(path, key, build string) error {
	return fmt.Errorf("%s: writer: %s must be an integer, not %q", path, key, build)
}

// allDigits reports a non-empty string of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
