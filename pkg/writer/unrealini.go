package writer

// The Unreal config sections and keys the writer touches, spelled as the
// reader spells them.
const (
	unrealSectionProject    = "/Script/EngineSettings.GeneralProjectSettings"
	unrealSectionAndroid    = "/Script/AndroidRuntimeSettings.AndroidRuntimeSettings"
	unrealKeyProjectVersion = "ProjectVersion"
	unrealKeyVersionDisplay = "VersionDisplayName"
	unrealKeyStoreVersion   = "StoreVersion"
)

// rewriteUnrealGameConfig sets a Config/DefaultGame.ini's ProjectVersion, the
// version the packaged game reports. The file declares no dependencies, so
// every edit is missing by definition.
//
// An array operation (+ProjectVersion=, .ProjectVersion=) is not this key and
// is left alone: the prefix makes it a different declaration, and Unreal
// resolves the two differently.
func rewriteUnrealGameConfig(path, version string, edits []Edit) (Result, error) {
	return rewriteUnrealKey(path, version, edits,
		func(section string) bool { return section == unrealSectionProject },
		func(key string) bool { return key == unrealKeyProjectVersion })
}

// rewriteUnrealEngineConfig sets the Android VersionDisplayName, the version
// string the store listing shows. StoreVersion beside it is the monotonic
// counter and is never touched here; SetBuild is where that is decided.
func rewriteUnrealEngineConfig(path, version string, edits []Edit) (Result, error) {
	return rewriteUnrealKey(path, version, edits,
		func(section string) bool { return section == unrealSectionAndroid },
		func(key string) bool { return key == unrealKeyVersionDisplay })
}

// rewriteUnrealKey is the splice the two Unreal config writers share.
func rewriteUnrealKey(path, version string, edits []Edit,
	want func(section string) bool, mine func(key string) bool) (Result, error) {
	res := Result{Missing: edits}
	if version == "" {
		return res, nil
	}
	if err := iniRefuse(path, version, unrealDialect); err != nil {
		return res, err
	}
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	before := sp.bytes()
	lines := sp.lines()
	found, changed := iniSplice(lines, unrealDialect, want,
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
	return res, sp.commit(func(out []byte) error {
		return iniVerify(before, out, unrealDialect, want, mine, version, found)
	})
}

// setUnrealEngineBuild writes the Android StoreVersion, which Google Play
// orders builds by and parses as an integer, so anything else is refused
// before the file is opened. The key is never created.
func setUnrealEngineBuild(path, build string) (Result, error) {
	var res Result
	if !allDigits(build) {
		return res, errNotAnInteger(path, unrealKeyStoreVersion, build)
	}
	sp, err := openSplicer(path)
	if err != nil {
		return res, err
	}
	want := func(section string) bool { return section == unrealSectionAndroid }
	mine := func(key string) bool { return key == unrealKeyStoreVersion }
	before := sp.bytes()
	lines := sp.lines()
	found, changed := iniSplice(lines, unrealDialect, want,
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
	return res, sp.commit(func(out []byte) error {
		return iniVerify(before, out, unrealDialect, want, mine, build, found)
	})
}
