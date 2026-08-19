package scanner

import "strings"

// The Godot keys and sections the scanner reads. Godot spells a key as a path
// inside its section, so the version of the project as a whole is
// application/config/version while an addon's is a plain version under
// [plugin].
const (
	godotSectionApplication  = "application"
	godotSectionPlugin       = "plugin"
	godotKeyConfigName       = "config/name"
	godotKeyConfigVersion    = "config/version"
	godotKeyPluginName       = "name"
	godotKeyPluginVersion    = "version"
	godotKeyPresetName       = "name"
	godotKeyVersionName      = "version/name"
	godotKeyVersionCode      = "version/code"
	godotKeyShortVersion     = "application/short_version"
	godotKeyApplicationVer   = "application/version"
	godotPresetSectionPrefix = "preset."
	godotPresetOptionsSuffix = ".options"
)

// parseGodotProject reads a project.godot: the project's name and version out
// of its [application] section. Godot declares no dependencies of its own,
// addons are vendored into addons/ and each carries its own plugin.cfg, so
// this is an identity-only manifest the way an Info.plist is. It feeds
// versioning, not the dependency graph.
//
// A project that never set config/version reads an empty version, which is
// normal: Godot does not write the key until somebody fills it in, and a
// project versioned only by its git tags never will.
//
// Godot 3 and Godot 4 differ in their config_version and in what else the file
// holds, but both spell these two keys the same way, so both parse here.
func parseGodotProject(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemGodot, Root: isRoot(rel)}
	iniScan(data, godotDialect, func(section, key, value string) {
		if section != godotSectionApplication {
			return
		}
		text, ok := iniUnquote(value, godotDialect)
		if !ok {
			return
		}
		switch key {
		case godotKeyConfigName:
			m.Name = strings.Clone(text)
		case godotKeyConfigVersion:
			m.Version = strings.Clone(text)
		}
	})
	return m, nil
}

// parseGodotPlugin reads an addon's plugin.cfg: the name and version of one
// Godot plugin, which is the closest thing the ecosystem has to a package
// manifest. Only the [plugin] section is read, so a file of the same name
// belonging to something else parses to an empty manifest rather than to a
// wrong one.
func parseGodotPlugin(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemGodot, Root: isRoot(rel)}
	iniScan(data, godotDialect, func(section, key, value string) {
		if section != godotSectionPlugin {
			return
		}
		text, ok := iniUnquote(value, godotDialect)
		if !ok {
			return
		}
		switch key {
		case godotKeyPluginName:
			m.Name = strings.Clone(text)
		case godotKeyPluginVersion:
			m.Version = strings.Clone(text)
		}
	})
	return m, nil
}

// parseGodotExportPresets reads an export_presets.cfg: the version and the
// store build counter Godot stamps into the packages it exports. The file
// carries one preset per platform, and the identity reported is the first
// preset's, because that is the one a reader asking "what does this project
// export as" means. The writer moves all of them.
//
// The file is frequently kept out of version control, since a preset can name
// a signing keystore. Its absence is normal and is not an error anywhere.
func parseGodotExportPresets(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemGodot, Root: isRoot(rel)}
	iniScan(data, godotDialect, func(section, key, value string) {
		switch {
		case isGodotPresetSection(section):
			if key == godotKeyPresetName && m.Name == "" {
				if text, ok := iniUnquote(value, godotDialect); ok {
					m.Name = strings.Clone(text)
				}
			}
		case isGodotPresetOptionsSection(section):
			switch key {
			case godotKeyVersionName, godotKeyShortVersion, godotKeyApplicationVer:
				if m.Version == "" {
					if text, ok := iniUnquote(value, godotDialect); ok {
						m.Version = strings.Clone(text)
					}
				}
			case godotKeyVersionCode:
				// The counter is a bare integer rather than a literal, so it
				// is read as written.
				if m.BuildNumber == "" {
					m.BuildNumber = strings.Clone(value)
				}
			}
		}
	})
	return m, nil
}

// isGodotPresetSection reports a [preset.N] header, which names a preset.
func isGodotPresetSection(section string) bool {
	return strings.HasPrefix(section, godotPresetSectionPrefix) &&
		!strings.HasSuffix(section, godotPresetOptionsSuffix)
}

// isGodotPresetOptionsSection reports a [preset.N.options] header, which holds
// the preset's version and counter.
func isGodotPresetOptionsSection(section string) bool {
	return strings.HasPrefix(section, godotPresetSectionPrefix) &&
		strings.HasSuffix(section, godotPresetOptionsSuffix)
}
