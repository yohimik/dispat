package scanner

// The game.project keys the scanner reads.
const (
	defoldSectionProject = "project"
	defoldKeyTitle       = "title"
	defoldKeyVersion     = "version"
)

// parseDefoldProject reads a game.project: the game's title as its name and
// the version beside it, both out of the [project] section.
//
// Defold declares its libraries as archive URLs, one per numbered key:
//
//	dependencies#0 = https://github.com/britzl/defold-input/archive/2.9.0.zip
//
// The version is inside the URL's path rather than in a field of its own, and
// which segment holds it differs by host and by how the archive was published.
// Reading a version out of that would be guesswork, so this is an
// identity-only manifest the way an Info.plist is: it feeds versioning, not
// the dependency graph.
func parseDefoldProject(rel string, data []byte) (Manifest, error) {
	return Manifest{
		Path:      rel,
		Ecosystem: EcosystemDefold,
		Name:      iniString(data, defoldDialect, defoldSectionProject, defoldKeyTitle),
		Version:   iniString(data, defoldDialect, defoldSectionProject, defoldKeyVersion),
		Root:      isRoot(rel),
	}, nil
}
