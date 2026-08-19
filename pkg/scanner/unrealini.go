package scanner

// The Unreal config sections and keys the scanner reads. Unreal names a
// section after the C++ class that loads it, so the project's own identity
// lives under GeneralProjectSettings and the Android store fields under the
// Android runtime settings.
const (
	unrealSectionProject    = "/Script/EngineSettings.GeneralProjectSettings"
	unrealSectionAndroid    = "/Script/AndroidRuntimeSettings.AndroidRuntimeSettings"
	unrealKeyProjectName    = "ProjectName"
	unrealKeyProjectVersion = "ProjectVersion"
	unrealKeyVersionDisplay = "VersionDisplayName"
	unrealKeyStoreVersion   = "StoreVersion"
)

// parseUnrealGameConfig reads a Config/DefaultGame.ini: the project's name and
// the ProjectVersion the engine reports at runtime and stamps into a packaged
// build. It declares no dependencies, so it is an identity-only manifest.
//
// Unreal writes ProjectVersion with four components (1.0.0.0). That is kept
// verbatim, the way every other declared version text is: what the file says
// is what the game reports, and normalising it here would make the two
// disagree.
func parseUnrealGameConfig(rel string, data []byte) (Manifest, error) {
	return Manifest{
		Path:      rel,
		Ecosystem: EcosystemUnreal,
		Name:      iniString(data, unrealDialect, unrealSectionProject, unrealKeyProjectName),
		Version:   iniString(data, unrealDialect, unrealSectionProject, unrealKeyProjectVersion),
		Root:      isRoot(rel),
	}, nil
}

// parseUnrealEngineConfig reads a Config/DefaultEngine.ini for the two Android
// store fields it carries: VersionDisplayName, the version players see, and
// StoreVersion, the monotonic integer Google Play orders builds by. The file
// holds a great deal else, all of it engine configuration rather than package
// identity, and none of it is read.
//
// The file names no package, so its Name is empty: a repository's engine
// configuration is not a thing other manifests depend on.
func parseUnrealEngineConfig(rel string, data []byte) (Manifest, error) {
	return Manifest{
		Path:        rel,
		Ecosystem:   EcosystemUnreal,
		Version:     iniString(data, unrealDialect, unrealSectionAndroid, unrealKeyVersionDisplay),
		BuildNumber: iniString(data, unrealDialect, unrealSectionAndroid, unrealKeyStoreVersion),
		Root:        isRoot(rel),
	}, nil
}
