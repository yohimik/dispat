package config

// The `runOutputs` key: the folders a sweep of one script writes, which a
// sweep executed on worker nodes carries back to the machine it was started
// on (CCME §28.10).
//
// It is the sweep's counterpart of `buildOutputs`, with two differences that
// decide everything below. It is root-only and read from the entry
// configuration alone, like `execution`, because a sweep is an invocation of
// the machine it was started on rather than a property of any one package.
// And its roots are relative to a repository rather than to a package,
// because the folder the tasks of one sweep write into is shared: ten
// packages' `tests` tasks write ten profiles into one `coverage/`, and that is
// why the orchestrator merges what comes back instead of replacing a folder.
//
// The shape of each root is checked where the file is read. Whether a root is
// a package folder, holds one, or overlaps a package's declared build output
// can only be asked once every package is known, so that is checked at
// discovery, which is what makes `dispat status` report it. Every refusal
// carries DiagnosticExecution, as the build keys' do.

import (
	"fmt"
	"path/filepath"

	lib "github.com/yohimik/dispat/pkg/config"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// runOutputMap fills the whole `runOutputs` object: a script name, and the
// roots its sweep writes. The names keep the case the file wrote them in and
// two names that fold together are refused, as they are in `scripts`.
func runOutputMap(dst *map[string][]string) setter {
	return lib.MapOf(dst, func(val any, at string) ([]string, error) {
		var roots []string
		err := strs(&roots)(val, at)
		return roots, err
	})
}

// validateRunOutputs checks the shape of every declared root: a path every
// machine a sweep reaches can write, relative to a repository root, and one
// list holding no root twice or one inside another.
func validateRunOutputs(c *File) error {
	for _, script := range sortedKeys(c.RunOutputs) {
		label := fmt.Sprintf("runOutputs[%q]", script)
		if err := validateRunOutputRoots(label, c.RunOutputs[script]); err != nil {
			return WithDiagnostic(DiagnosticExecution, err)
		}
	}
	return nil
}

// validateRunOutputRoots checks one script's roots, each on its own and then
// the list against itself.
func validateRunOutputRoots(label string, roots []string) error {
	claims := make([]outputRootClaim, 0, len(roots))
	for i, declared := range roots {
		clean, err := cleanRunOutput(label, declared)
		if err != nil {
			return err
		}
		claims = append(claims, outputRootClaim{folded: lib.Fold(clean), index: i})
	}
	owner, other, isOverlapping := findOverlappingClaim(claims)
	if !isOverlapping {
		return nil
	}
	return fmt.Errorf("%s: %q and %q are one root, or one inside the other; name each folder the sweep writes once",
		label, roots[owner], roots[other])
}

// cleanRunOutput holds one root to the rules a build output root is held to,
// worded for a path relative to the repository root.
func cleanRunOutput(label, declared string) (string, error) {
	clean, fault := findRootShapeFault(declared)
	switch fault {
	case rootShapeSound:
		return clean, nil
	case rootShapeEmpty:
		return "", fmt.Errorf("%s: an empty path names no folder", label)
	case rootShapeNul:
		return "", fmt.Errorf("%s: a run output path holds no NUL byte", label)
	case rootShapeBackslash:
		return "", fmt.Errorf(
			"%s: %q is written with backslashes; run outputs are slash-separated on every platform", label, declared)
	case rootShapeColon:
		return "", fmt.Errorf(
			"%s: %q holds a colon; a run output names a folder inside the repository, never a drive or a remote",
			label, declared)
	case rootShapeAbsolute:
		return "", fmt.Errorf("%s: %q is absolute; run outputs are relative to the repository root", label, declared)
	case rootShapeParent:
		return "", fmt.Errorf("%s: %q leaves the repository; a sweep writes inside the checkout it runs in",
			label, declared)
	case rootShapeGitFolder:
		return "", fmt.Errorf("%s: %q reaches into %s; a run output is what a script wrote, not repository metadata",
			label, declared, gitFolderName)
	default:
		return "", fmt.Errorf(
			"%s: %q names the repository root itself; declare the folders the script writes inside it", label, declared)
	}
}

// checkRunOutputRoots refuses a declared sweep root that is a package folder,
// holds one, or overlaps a package's declared build output root, in every
// repository a swept package may belong to.
//
// A package folder is refused because what comes back is merged into the
// orchestrator's checkout, and a sweep writing into a package's own folder
// would be a sweep editing sources. A build output root is refused because the
// two keys install differently: a build output root is replaced whole, a
// sweep's root is merged file by file, and one folder cannot be both.
//
// The roots are resolved against the repository of each package rather than
// against the entry's alone, because that is where a sweep task writes them:
// a source of a composed workspace has a `coverage/` of its own.
func checkRunOutputRoots(runOutputs map[string][]string, pkgs []*model.Package, fallbackRoot string) error {
	if len(runOutputs) == 0 {
		return nil
	}
	builds := collectDeclaredOutputRoots(pkgs)
	for _, repositoryRoot := range collectRepositoryRoots(pkgs, fallbackRoot) {
		for _, script := range sortedKeys(runOutputs) {
			for _, declared := range runOutputs[script] {
				if err := checkRunOutputRoot(runOutputRoot{
					script: script, declared: declared, repositoryRoot: repositoryRoot,
				}, pkgs, builds); err != nil {
					return WithDiagnostic(DiagnosticExecution, err)
				}
			}
		}
	}
	return nil
}

// runOutputRoot is one declared sweep root resolved against one repository:
// which script declared it, what the file wrote, and the repository it is
// relative to.
type runOutputRoot struct {
	script         string
	declared       string
	repositoryRoot string
}

// checkRunOutputRoot is the one root's three questions, in the order a reader
// is best told about them.
func checkRunOutputRoot(root runOutputRoot, pkgs []*model.Package, builds []declaredOutputRoot) error {
	folder := filepath.Join(root.repositoryRoot, filepath.FromSlash(root.declared))
	folded := lib.Fold(folder)
	named := func(path string) string { return formatRepositoryPath(root.repositoryRoot, path) }
	for _, p := range pkgs {
		if !isFolderWithin(lib.Fold(p.Dir), folded) {
			continue
		}
		return fmt.Errorf("config: runOutputs[%q]: %q (%s) is or holds the folder of package %q (%s); "+
			"what a sweep carries back is merged into the checkout, so its roots must not reach a package's sources",
			root.script, root.declared, named(folder), p.Name, named(p.Dir))
	}
	for _, build := range builds {
		if !isFolderWithin(build.folded, folded) && !isFolderWithin(folded, build.folded) {
			continue
		}
		return fmt.Errorf("config: runOutputs[%q]: %q (%s) overlaps the build output %q of package %q (%s); "+
			"a build output root is replaced whole and a sweep's root is merged file by file, so one folder cannot be both",
			root.script, root.declared, named(folder), build.declared, build.packageName, named(build.path))
	}
	return nil
}

// collectRepositoryRoots is every repository a swept package may belong to:
// each package's own, and the folder discovery ran in for a package that
// records none, which is every package of a single history.
func collectRepositoryRoots(pkgs []*model.Package, fallbackRoot string) []string {
	seen := map[string]bool{}
	var roots []string
	for _, p := range pkgs {
		root := p.RepoRoot
		if root == "" {
			root = fallbackRoot
		}
		if seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		roots = append(roots, fallbackRoot)
	}
	return roots
}

// formatRepositoryPath renders a resolved folder relative to the repository
// it was resolved against, slash-separated, so that a refusal reads like the
// file it is about rather than like this machine's temporary directory.
func formatRepositoryPath(repositoryRoot, path string) string {
	relative, err := filepath.Rel(repositoryRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}
