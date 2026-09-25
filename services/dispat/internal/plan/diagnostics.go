// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import "fmt"

// Level separates advisory diagnostics from ones that must fail the run.
type Level int

const (
	LevelWarn Level = iota
	LevelError
)

// Diagnostic is one reportable observation about the plan.
type Diagnostic struct {
	Code    string
	Level   Level
	Pkg     string
	Commit  string
	Message string
	// Repository identifies the commit's owner in a composed workspace. It
	// remains separate from Commit so public commit abbreviations are stable.
	Repository string `json:",omitempty"`
}

func (d Diagnostic) String() string {
	if d.Pkg == "" {
		return fmt.Sprintf("%s: %s", d.Code, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s", d.Code, d.Pkg, d.Message)
}

// ---------------------------------------------------------------------------
// diagnostics
// ---------------------------------------------------------------------------

func (cp *computation) warn(code, pkg, commit, msg string) {
	repository, revision := splitHistoryKey(commit)
	cp.diags = append(cp.diags, Diagnostic{Code: code, Level: LevelWarn, Pkg: pkg, Commit: revision, Repository: repository, Message: msg})
}

func (cp *computation) err(code, pkg, commit, msg string) {
	repository, revision := splitHistoryKey(commit)
	cp.diags = append(cp.diags, Diagnostic{Code: code, Level: LevelError, Pkg: pkg, Commit: revision, Repository: repository, Message: msg})
}

// relWarn attaches a warning to a package's release as well as to the run.
func (cp *computation) relWarn(pkg, code, commit, msg string) {
	repository, revision := splitHistoryKey(commit)
	d := Diagnostic{Code: code, Level: LevelWarn, Pkg: pkg, Commit: revision, Repository: repository, Message: msg}
	if rel := cp.rel[pkg]; rel != nil {
		rel.Diagnostics = append(rel.Diagnostics, d)
	}
	cp.diags = append(cp.diags, d)
}

func (cp *computation) pkgWarn(rel *Release, code, commit, msg string) {
	cp.relWarn(rel.Pkg.Name, code, commit, msg)
}

func (cp *computation) pkgErr(rel *Release, code, msg string) {
	d := Diagnostic{Code: code, Level: LevelError, Pkg: rel.Pkg.Name, Message: msg}
	rel.Diagnostics = append(rel.Diagnostics, d)
	cp.diags = append(cp.diags, d)
}
