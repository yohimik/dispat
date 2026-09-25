// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import "strings"

// GitHubExport is one of the outputs with a consumer inside dispat: a package
// that exports it gets a GitHub release (when the recorder is enabled), with
// the value read as a whitespace-separated list of files to attach; a package
// that does not is skipped by the recorder. Unlike ordinary outputs it is
// exported under — and travels to later scripts as — this full name.
const GitHubExport = "DISPAT_EXPORT_GITHUB"

// PackageCommitExportPrefix is the other output convention with a consumer
// inside dispat. A release script that exports PACKAGE_<KEY>=<commitHash>
// (reaching later scripts as DISPAT_OUTPUT_PACKAGE_<KEY>), where <KEY> is the
// package's own EnvKey, pins that package's release: the tag is created at
// the exported commit instead of the run's commit (the release commit in
// commit mode), and the package's GitHub release carries the hash as its
// commit and target_commitish. Meant for packages whose release scripts
// produce their own commit (a subtree push, a generated repository) that the
// tag should point at.
const PackageCommitExportPrefix = "PACKAGE_"

// EnvKey turns a package name into the fragment it occupies inside a
// DISPAT_* variable name. Environment variable names admit far less than
// package names do — "@acme/ui" is a fine package and an impossible
// variable — so anything outside [A-Z0-9] becomes "_" and letters are
// uppercased. The key only has to be addressable, not reversible.
func EnvKey(name string) string {
	b := []byte(strings.ToUpper(name))
	for i, c := range b {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			b[i] = '_'
		}
	}
	return string(b)
}

// ExportedCommit returns the commit hash the package's scripts pinned this
// release to via the PACKAGE_<KEY> export, or "" when none was exported.
func (r *Release) ExportedCommit() string {
	if r.Pkg == nil {
		return ""
	}
	if v, ok := r.Output(PackageCommitExportPrefix + EnvKey(r.Pkg.Name)); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// Output is one NAME=value pair a script exported through its DISPAT_OUTPUT
// file.
type Output struct {
	Name  string
	Value string
	// Source names the script that exported (or last re-exported) the value,
	// as "<package>:<stage>" — "core:build", "base:run:lint" — or
	// "<space>:login" for the space-level login script.
	Source string
}

// Output returns the value of the named script output, if exported.
func (r *Release) Output(name string) (string, bool) {
	for _, o := range r.Outputs {
		if o.Name == name {
			return o.Value, true
		}
	}
	return "", false
}
