package writer

// The game.project section and key the writer touches.
const (
	defoldSectionProject = "project"
	defoldKeyVersion     = "version"
)

// rewriteDefoldProject sets a game.project's version. Defold declares its
// libraries as archive URLs whose version sits inside the URL path, which is
// not a version text a writer can replace without rebuilding somebody else's
// download link, so every edit is missing here and only the project's own
// version is written.
func rewriteDefoldProject(path, version string, edits []Edit) (Result, error) {
	res := Result{Missing: edits}
	if version == "" {
		return res, nil
	}
	if err := iniRefuse(path, version, defoldDialect); err != nil {
		return res, err
	}
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	want := func(section string) bool { return section == defoldSectionProject }
	mine := func(key string) bool { return key == defoldKeyVersion }
	before := sp.bytes()
	lines := sp.lines()
	found, changed := iniSplice(lines, defoldDialect, want,
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
		return iniVerify(before, out, defoldDialect, want, mine, version, found)
	})
}
