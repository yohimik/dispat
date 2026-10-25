package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/semver"
)

type semverOutput struct {
	Version    string   `json:"version"`
	Major      string   `json:"major"`
	Minor      string   `json:"minor"`
	Patch      string   `json:"patch"`
	Prerelease []string `json:"prerelease"`
	Build      []string `json:"build"`
}

func (r *runner) runSemver() int {
	if r.inv.semverAction == "parse" &&
		(r.fs.Changed("pre-release") || r.fs.Changed("build-metadata")) {
		fmt.Fprintln(r.stderr, "dispat semver parse: --pre-release and --build-metadata belong to build")
		return 2
	}
	raw := r.inv.semverValues[0]
	if r.inv.semverAction == "build" {
		raw = buildSemverSpelling(semverSpelling{
			core: r.inv.semverValues, preSet: r.fs.Changed("pre-release"), pre: *r.o.semverPrerelease,
			buildSet: r.fs.Changed("build-metadata"), build: *r.o.semverBuildMetadata,
		})
	}
	parts, err := semver.ParseVersionParts(raw)
	if err != nil {
		fmt.Fprintf(r.stderr, "dispat semver: %v\n", err)
		return 2
	}
	if parts.Prerelease == nil {
		parts.Prerelease = []string{}
	}
	if parts.Build == nil {
		parts.Build = []string{}
	}
	out := semverOutput{
		Version: parts.Raw, Major: parts.Major, Minor: parts.Minor, Patch: parts.Patch,
		Prerelease: parts.Prerelease, Build: parts.Build,
	}
	if err := json.NewEncoder(r.stdout).Encode(out); err != nil {
		fmt.Fprintf(r.stderr, "dispat semver: write JSON: %v\n", err)
		return 1
	}
	return 0
}

type semverSpelling struct {
	core             []string
	preSet, buildSet bool
	pre, build       string
}

func buildSemverSpelling(input semverSpelling) string {
	var spelling strings.Builder
	spelling.Grow(len(input.core[0]) + len(input.core[1]) + len(input.core[2]) + len(input.pre) + len(input.build) + 4)
	spelling.WriteString(input.core[0])
	spelling.WriteByte('.')
	spelling.WriteString(input.core[1])
	spelling.WriteByte('.')
	spelling.WriteString(input.core[2])
	if input.preSet {
		spelling.WriteByte('-')
		spelling.WriteString(input.pre)
	}
	if input.buildSet {
		spelling.WriteByte('+')
		spelling.WriteString(input.build)
	}
	return spelling.String()
}
