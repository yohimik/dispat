package config

// The `buildOutputs` and `buildPlatforms` keys: what a package's build
// produces, and which machines may run that build.
//
// A build product is normally ignored by Git, so a checkout says nothing
// about it and a machine that did not run the build cannot learn what to ask
// for. Declaring the paths is how a package says what its build leaves
// behind, and the declaration is the whole of what may travel: the shape of
// each entry is checked here, and so is the one question no single level can
// answer for itself, whether two packages have claimed one folder.
//
// Both keys ride the ordinary ladder (root, space, space folder file,
// package, package folder file) and replace whole, like `aliasTags` and
// `webhooks` beside them. Every refusal carries DiagnosticExecution, because
// what they describe is how a run is executed rather than what it releases.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	lib "github.com/yohimik/dispat/pkg/config"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// buildPlatformPattern is the whole vocabulary of a platform: Go's GOOS/GOARCH
// spelling, which is what a node reports about itself and therefore the only
// spelling two machines can be held to agree on. Both halves are lower-case
// ASCII because every value Go defines is.
var buildPlatformPattern = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)

// gitFolderName is the one folder name a build output may never name. It is
// refused as a component rather than as a prefix: a build that writes into a
// repository's own metadata is a build that rewrites history on whichever
// machine installs its outputs.
const gitFolderName = ".git"

// buildKeyLabel locates one of the two keys for the reader: the key alone at
// the root, where there is no level above it to name, and the key under the
// level that stated it everywhere else.
func buildKeyLabel(level, key string) string {
	if level == "" {
		return key
	}
	return level + ": " + key
}

// validateBuildKeys checks one level's two build keys. Every level calls this
// rather than the two halves, so no level can come to be held to one of the
// rules and not the other.
func validateBuildKeys(level string, outputs, platforms []string) error {
	if err := validateBuildOutputs(buildKeyLabel(level, "buildOutputs"), outputs); err != nil {
		return WithDiagnostic(DiagnosticExecution, err)
	}
	if err := validateBuildPlatforms(buildKeyLabel(level, "buildPlatforms"), platforms); err != nil {
		return WithDiagnostic(DiagnosticExecution, err)
	}
	return nil
}

// validateBuildOutputs checks one level's declared build outputs: each entry
// on its own, and then the list against itself.
//
// The entry rules are one rule seen from several sides. The path has to name
// a place inside the package folder on every machine a run may reach, so
// anything that is not a plain relative slash-separated path is refused here
// rather than discovered as a write outside a package on the node that
// installed it.
func validateBuildOutputs(label string, outputs []string) error {
	claims := make([]outputRootClaim, 0, len(outputs))
	for i, declared := range outputs {
		clean, err := cleanBuildOutput(label, declared)
		if err != nil {
			return err
		}
		claims = append(claims, outputRootClaim{folded: lib.Fold(clean), index: i})
	}
	owner, other, isOverlapping := findOverlappingClaim(claims)
	if !isOverlapping {
		return nil
	}
	return fmt.Errorf(
		"%s: %q and %q are one build output root, or one inside the other; a root travels whole, "+
			"so the narrower entry is already covered by the wider one",
		label, outputs[owner], outputs[other])
}

// cleanBuildOutput holds one entry to the shape every machine can read it as,
// and returns the path the duplicate and nesting rules compare: the entry
// with its trailing slash and its redundant separators taken off, so that
// `dist` and `dist/` are recognised as the one root they are.
func cleanBuildOutput(label, declared string) (string, error) {
	if declared == "" {
		return "", fmt.Errorf("%s: an empty path names no build output", label)
	}
	if strings.ContainsRune(declared, 0) {
		return "", fmt.Errorf("%s: a build output path holds no NUL byte", label)
	}
	if strings.Contains(declared, `\`) {
		return "", fmt.Errorf(
			"%s: %q is written with backslashes; build outputs are slash-separated on every platform", label, declared)
	}
	if strings.Contains(declared, ":") {
		return "", fmt.Errorf(
			"%s: %q holds a colon; a build output names a path inside the package folder, never a drive or a remote",
			label, declared)
	}
	if strings.HasPrefix(declared, "/") || filepath.IsAbs(declared) {
		return "", fmt.Errorf("%s: %q is absolute; build outputs are relative to the package folder", label, declared)
	}
	for _, component := range strings.Split(declared, "/") {
		if component == ".." {
			return "", fmt.Errorf(
				"%s: %q leaves the package folder; a package declares its own outputs and no one else's", label, declared)
		}
		if strings.EqualFold(component, gitFolderName) {
			return "", fmt.Errorf("%s: %q reaches into %s; a build output is a build product, not repository metadata",
				label, declared, gitFolderName)
		}
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(declared)))
	if clean == "." {
		// A root is installed by replacing the folder it names, so the package
		// folder as a root would replace the consumer's whole checkout of the
		// package, sources and nested packages included.
		return "", fmt.Errorf(
			"%s: %q names the package folder itself; declare the folders the build writes inside it", label, declared)
	}
	return clean, nil
}

// validateBuildPlatforms checks one level's platform list: each entry is a
// platform, and no entry is stated twice. A repeated platform is refused
// rather than folded away, because a list that cannot be read literally is a
// list that quietly means something other than what it says.
func validateBuildPlatforms(label string, platforms []string) error {
	stated := make(map[string]int, len(platforms))
	for i, platform := range platforms {
		if !buildPlatformPattern.MatchString(platform) {
			return fmt.Errorf(
				`%s: %q is not a platform; write os/arch in Go's spelling, such as "linux/amd64"`, label, platform)
		}
		if previous, isDuplicate := stated[platform]; isDuplicate {
			return fmt.Errorf("%s: %q is already stated at index %d", label, platform, previous)
		}
		stated[platform] = i
	}
	return nil
}

// outputRootClaim is one claim on one folder: the folder as the comparison
// spells it, and whatever index the caller identifies the claim by.
//
// The folded spelling is what is compared, because a checkout on a
// case-insensitive filesystem merges two spellings of one folder into one,
// and a pair that is two roots on one machine and one root on the next is a
// pair no run could be correct under.
type outputRootClaim struct {
	folded string
	index  int
}

// findOverlappingClaim returns the first pair of claims where one owns the
// other's folder: the same folder twice, or one folder inside another.
//
// The claims are sorted first, because a folder always sorts ahead of
// everything inside it, and that is what lets one walk up from each new claim
// find an owner however deep the nesting goes. Equal folders keep the
// caller's own order, so a collision is always reported the same way round.
func findOverlappingClaim(claims []outputRootClaim) (int, int, bool) {
	sorted := append([]outputRootClaim(nil), claims...)
	sort.Slice(sorted, func(a, b int) bool {
		if sorted[a].folded != sorted[b].folded {
			return sorted[a].folded < sorted[b].folded
		}
		return sorted[a].index < sorted[b].index
	})
	owners := make(map[string]int, len(sorted))
	for _, claim := range sorted {
		if owner, isTaken := findRootOwner(owners, claim.folded); isTaken {
			return owner, claim.index, true
		}
		owners[claim.folded] = claim.index
	}
	return 0, 0, false
}

// findRootOwner reports which claimed folder owns root: the folder itself, or
// one above it.
func findRootOwner(owners map[string]int, root string) (int, bool) {
	if owner, isTaken := owners[root]; isTaken {
		return owner, true
	}
	for parent := parentRoot(root); parent != ""; parent = parentRoot(parent) {
		if owner, isTaken := owners[parent]; isTaken {
			return owner, true
		}
	}
	return 0, false
}

// parentRoot is the folder holding root, or "" once the walk has nothing left
// above it. It stops at the relative and at the absolute top rather than at a
// configured root, so one walk serves a package-relative entry and a resolved
// absolute one.
func parentRoot(root string) string {
	parent := filepath.Dir(root)
	if parent == root || parent == "." || parent == string(filepath.Separator) {
		return ""
	}
	return parent
}

// declaredOutputRoot is one package's one declared build output, resolved to
// the folder it names: what the file wrote, where that folder is, and which
// package claimed it. The folded path is what the overlap check compares, and
// the rest is what the refusal has to be able to say.
type declaredOutputRoot struct {
	packageName string
	declared    string
	path        string
	folded      string
}

// checkBuildOutputRoots refuses a workspace where two packages have claimed
// one folder: the same folder twice, or one package's root inside another's.
//
// It can only be asked once every package is known, because package folders
// may nest and a folder's owner is the nearest package above it. A declared
// output is captured, transported and installed whole, so a folder with two
// owners is a folder whose contents would be attributed to whichever build
// finished last, and one package's files would be installed as another's
// inputs. Refusing it at discovery is what makes `dispat status` report it,
// long before anything is executed anywhere.
func (d *discovery) checkBuildOutputRoots() error {
	roots := collectDeclaredOutputRoots(d.pkgs)
	claims := make([]outputRootClaim, 0, len(roots))
	for i, root := range roots {
		claims = append(claims, outputRootClaim{folded: root.folded, index: i})
	}
	owner, other, isOverlapping := findOverlappingClaim(claims)
	if isOverlapping {
		return d.refuseOverlappingOutputs(roots[owner], roots[other])
	}
	return d.checkBuildOutputRootsHoldNoPackage(roots)
}

// checkBuildOutputRootsHoldNoPackage refuses a declared root that holds
// another package's folder. Package folders may nest, and a root is installed
// by replacing the folder it names, so installing such a root on a consumer's
// node would replace the nested package's checkout with whatever the outer
// build left there, whether or not the nested package declares anything.
func (d *discovery) checkBuildOutputRootsHoldNoPackage(roots []declaredOutputRoot) error {
	for _, root := range roots {
		for _, nested := range d.pkgs {
			if nested.Name == root.packageName || !isFolderWithin(lib.Fold(nested.Dir), root.folded) {
				continue
			}
			return WithDiagnostic(DiagnosticExecution, fmt.Errorf(
				"config: buildOutputs: package %q declares %q (%s), which holds the folder of package %q (%s); "+
					"a declared build output is installed whole, so it cannot contain another package",
				root.packageName, root.declared, d.repositoryPath(root.path),
				nested.Name, d.repositoryPath(nested.Dir)))
		}
	}
	return nil
}

// isFolderWithin reports whether folder is root or lies inside it. Both are
// folded absolute paths, compared by component so that `dist-old` is not
// taken for a folder inside `dist`.
func isFolderWithin(folder, root string) bool {
	return folder == root || strings.HasPrefix(folder, root+string(filepath.Separator))
}

// collectDeclaredOutputRoots resolves every package's declared outputs
// against its own folder. Resolving them is what makes two repositories of a
// composed workspace independent without a rule saying so: two packages
// collide when the folders they name are one folder, and folders in separate
// checkouts are not.
func collectDeclaredOutputRoots(pkgs []*model.Package) []declaredOutputRoot {
	var roots []declaredOutputRoot
	for _, p := range pkgs {
		for _, declared := range p.Space.BuildOutputs {
			path := filepath.Join(p.Dir, filepath.FromSlash(declared))
			roots = append(roots, declaredOutputRoot{
				packageName: p.Name, declared: declared, path: path, folded: lib.Fold(path),
			})
		}
	}
	return roots
}

// refuseOverlappingOutputs names both packages, both entries and both
// folders, because the reader has to be told which of the two declarations to
// change and the two entries on their own can look unrelated: a `dist` inside
// a package that itself sits inside another package's declared folder is a
// collision neither file mentions.
func (d *discovery) refuseOverlappingOutputs(owner, other declaredOutputRoot) error {
	return WithDiagnostic(DiagnosticExecution, fmt.Errorf(
		"config: buildOutputs: package %q declares %q (%s) and package %q declares %q (%s); %s, "+
			"and a declared build output is captured and installed whole, so one folder cannot have two owners",
		owner.packageName, owner.declared, d.repositoryPath(owner.path),
		other.packageName, other.declared, d.repositoryPath(other.path),
		describeRootOverlap(owner, other)))
}

// describeRootOverlap says which of the two shapes of overlap this is, so the
// reader is told whether the two declarations name one folder or one folder
// inside the other.
func describeRootOverlap(owner, other declaredOutputRoot) string {
	if owner.folded == other.folded {
		return "they name the same folder"
	}
	return "the second sits inside the first"
}

// repositoryPath renders a resolved folder the way the configuration names
// things: relative to the repository root and slash-separated, so a refusal
// reads like the file it is about rather than like this machine's temporary
// directory.
func (d *discovery) repositoryPath(path string) string {
	return filepath.ToSlash(strings.TrimPrefix(path, d.root+string(filepath.Separator)))
}
