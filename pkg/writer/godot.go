package writer

import "strings"

// The Godot keys and sections the writer touches, spelled as the reader spells
// them.
const (
	godotSectionApplication  = "application"
	godotSectionPlugin       = "plugin"
	godotKeyConfigVersion    = "config/version"
	godotKeyPluginVersion    = "version"
	godotKeyVersionName      = "version/name"
	godotKeyVersionCode      = "version/code"
	godotKeyShortVersion     = "application/short_version"
	godotKeyApplicationVer   = "application/version"
	godotPresetSectionPrefix = "preset."
	godotPresetOptionsSuffix = ".options"
)

// rewriteGodotProject sets a project.godot's config/version. Godot declares no
// dependencies, so every edit is missing by definition, and the key is only
// ever updated rather than created: a project that never wrote one has decided
// its version lives elsewhere, and inventing the key would overrule that.
func rewriteGodotProject(path, version string, edits []Edit) (Result, error) {
	return rewriteGodotKey(path, version, edits,
		func(section string) bool { return section == godotSectionApplication },
		func(key string) bool { return key == godotKeyConfigVersion })
}

// rewriteGodotPlugin sets an addon's plugin.cfg version, under the same terms.
func rewriteGodotPlugin(path, version string, edits []Edit) (Result, error) {
	return rewriteGodotKey(path, version, edits,
		func(section string) bool { return section == godotSectionPlugin },
		func(key string) bool { return key == godotKeyPluginVersion })
}

// rewriteGodotExportPresets sets the marketing version of every export preset:
// version/name for Android, application/short_version and application/version
// for Apple. Every preset is written, not just the first, because a project
// exporting to three stores that stamped one of them would ship two stale
// version strings and nothing would say so.
//
// version/code is deliberately untouched. It is the store's monotonic counter,
// and SetBuild is where that decision is made.
func rewriteGodotExportPresets(path, version string, edits []Edit) (Result, error) {
	return rewriteGodotKey(path, version, edits, isGodotPresetOptionsSection,
		func(key string) bool {
			switch key {
			case godotKeyVersionName, godotKeyShortVersion, godotKeyApplicationVer:
				return true
			}
			return false
		})
}

// rewriteGodotKey is the splice the three Godot writers share: set the value
// of the keys mine accepts, in the sections want accepts, and prove the result
// still reads as the config file it was.
func rewriteGodotKey(path, version string, edits []Edit,
	want func(section string) bool, mine func(key string) bool) (Result, error) {
	res := Result{Missing: edits}
	if version == "" {
		return res, nil
	}
	if err := iniRefuse(path, version, godotDialect); err != nil {
		return res, err
	}
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	lines := sp.lines()
	found, changed := iniSplice(lines, godotDialect, want,
		func(key, _ string, _ bool) (string, bool) {
			if !mine(key) {
				return "", false
			}
			return version, true
		})
	if changed == 0 {
		return res, nil
	}
	sp.setLines(lines)
	res.VersionWritten = true
	before := sp.bytes()
	return res, sp.commit(func(out []byte) error {
		return iniVerify(before, out, godotDialect, want, mine, version, found)
	})
}

// setGodotExportBuild writes version/code in every export preset. Godot parses
// it as an integer, so anything else is refused before the file is opened, the
// way the Android manifest writer refuses a non-integer versionCode. The key
// is never created: a project that exports without a counter has decided so.
func setGodotExportBuild(path, build string) (Result, error) {
	var res Result
	if !allDigits(build) {
		return res, errNotAnInteger(path, godotKeyVersionCode, build)
	}
	sp, err := openSplicer(path)
	if err != nil {
		return res, err
	}
	mine := func(key string) bool { return key == godotKeyVersionCode }
	lines := sp.lines()
	found, changed := iniSplice(lines, godotDialect, isGodotPresetOptionsSection,
		func(key, _ string, _ bool) (string, bool) {
			if !mine(key) {
				return "", false
			}
			return build, true
		})
	if changed == 0 {
		return res, nil
	}
	sp.setLines(lines)
	res.BuildWritten = true
	before := sp.bytes()
	return res, sp.commit(func(out []byte) error {
		return iniVerify(before, out, godotDialect, isGodotPresetOptionsSection, mine, build, found)
	})
}

// isGodotPresetOptionsSection reports a [preset.N.options] header, the section
// holding a preset's version and counter.
func isGodotPresetOptionsSection(section string) bool {
	return strings.HasPrefix(section, godotPresetSectionPrefix) &&
		strings.HasSuffix(section, godotPresetOptionsSuffix)
}
