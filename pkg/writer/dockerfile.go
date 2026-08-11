package writer

import (
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// rewriteDockerfile sets the tag of every image reference matching an edit —
// the base a stage builds on, and the image a COPY --from or a RUN --mount
// pulls files out of. Both are dependencies of the image this file builds, so
// both are reconciled, and a file naming the same base twice has both
// occurrences brought to the same version.
//
// A Dockerfile declares no version of its own. What it builds is named on the
// command line, not in the file, so there is nothing else to write.
//
// The references are located by manifest.DockerfileRefs, the same walk the
// scanner reads them with, so the writer cannot fail to find a dependency the
// scanner reported.
func rewriteDockerfile(path string, edits []Edit) (Result, error) {
	rep, err := openReplacer(path)
	if err != nil {
		return Result{}, err
	}
	var (
		res   Result
		lines = rep.lines()
		w     = newImageTagWriter(path, edits)
	)
	for _, ref := range manifest.DockerfileRefs(lines) {
		if err := w.match(ref.Line, ref.Start, ref.Text); err != nil {
			return res, err
		}
	}
	changed := w.apply(lines)
	w.fill(&res)
	if changed {
		rep.setLines(lines)
	}
	return res, rep.commit(func(out []byte) error {
		after := manifest.DockerfileRefs(strings.Split(string(out), "\n"))
		texts := make([]string, 0, len(after))
		for _, ref := range after {
			texts = append(texts, ref.Text)
		}
		return verifyImageTags(res.Applied, texts)
	})
}
