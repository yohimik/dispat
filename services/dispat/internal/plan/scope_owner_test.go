// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"math/rand"
	"path"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// underDir reports whether file sits inside dir, respecting path boundaries so
// that /r/libs/core-extra is not mistaken for a file of /r/libs/core. It is
// the containment ownerOf answers by probing, kept here as its definition.
func underDir(file, dir string) bool {
	if dir == "" || dir == "." {
		// A package rooted at the repository root owns everything; this only
		// arises in tests and degenerate configurations.
		return true
	}
	clean := path.Clean(dir)
	if file == clean {
		return true
	}
	return strings.HasPrefix(file, strings.TrimSuffix(clean, "/")+"/")
}

// ownerByScan is the rule of §6.2 written as its definition: compare the file
// against every scope folder and keep the strictly longest match. ownerOf must
// agree with it on every input; it exists only to be obviously right.
func ownerByScan(cp *computation, rec *commitRec, full string) *scopeDir {
	var owner *scopeDir
	for i := range cp.scopeDirs {
		sd := &cp.scopeDirs[i]
		if !cp.commitCanDerive(rec, sd.pkg) || !underDir(full, sd.dir) {
			continue
		}
		if owner == nil || len(sd.dir) > len(owner.dir) {
			owner = sd
		}
	}
	return owner
}

// TestOwnerOfAgreesWithTheScan draws nested, shared, root-level and catch-all
// scope folders over a tiny alphabet, so that prefixes, siblings that merely
// share a spelling ("a" and "ab"), folders shared by two packages and the
// "/", "." and "" folders all collide often.
func TestOwnerOfAgreesWithTheScan(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	segment := func() string { return []string{"a", "b", "ab", "c"}[rng.Intn(4)] }
	somePath := func(absolute bool) string {
		p := ""
		for n := rng.Intn(4); n > 0; n-- {
			p = path.Join(p, segment())
		}
		if absolute {
			return "/" + p
		}
		return p
	}
	for round := 0; round < 400; round++ {
		absolute := rng.Intn(2) == 0
		var pkgs []*model.Package
		for n := 1 + rng.Intn(6); n > 0; n-- {
			dir := somePath(absolute)
			switch rng.Intn(8) {
			case 0:
				dir = "."
			case 1:
				dir = ""
			}
			pkgs = append(pkgs, &model.Package{Name: string(rune('p' + len(pkgs))), Dir: dir})
		}
		cp := &computation{pkgs: pkgs, log: zerolog.Nop()}
		cp.prepareScopeDirs()
		rec := &commitRec{}
		for n := 0; n < 40; n++ {
			full := path.Clean(path.Join(somePath(absolute), segment()))
			want, got := ownerByScan(cp, rec, full), cp.ownerOf(rec, full)
			require.Equalf(t, want, got, "owner of %q among %v", full, cp.scopeDirs)
		}
	}
}
