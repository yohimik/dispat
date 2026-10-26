// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"fmt"
	"path"
	"strings"
)

// DeclaredOutputPath is the path portion of a typed, signed export. A normal
// exported string stays a string, even if its spelling resembles a path.
// The transport binds repository and package identity beside this value.
type DeclaredOutputPath struct {
	Root     string `json:"root"`
	Relative string `json:"relative"`
}

// IdentifyDeclaredOutputPath classifies a value only when it is an absolute
// path under one of the package's declared output roots. The longest matching
// root wins, so nested declarations have one unambiguous representation.
func IdentifyDeclaredOutputPath(value, sourceRoot string, roots []string) (DeclaredOutputPath, bool) {
	if hasParentOrNUL(value) {
		return DeclaredOutputPath{}, declaredOutputPathUnmatchedState
	}
	relative, ok := relativeSourcePath(value, sourceRoot)
	if !ok {
		return DeclaredOutputPath{}, declaredOutputPathUnmatchedState
	}
	best := DeclaredOutputPath{}
	for _, stated := range roots {
		root, valid := canonicalDeclaredRoot(stated)
		if !valid || relative != root && !strings.HasPrefix(relative, root+declaredOutputPathSeparatorText) || len(root) <= len(best.Root) {
			continue
		}
		remainder := strings.TrimPrefix(relative, root)
		remainder = strings.TrimPrefix(remainder, declaredOutputPathSeparatorText)
		if remainder == declaredOutputAbsentPathText {
			remainder = declaredOutputRootRelativePath
		}
		if !validRelativePart(remainder) {
			continue
		}
		best = DeclaredOutputPath{Root: root, Relative: remainder}
	}
	return best, best.Root != declaredOutputAbsentPathText
}

// RehomeDeclaredOutputPath materializes a verified typed export under one
// authoritative or private package root. It validates the declaration again
// before joining paths, so a signed foreign or traversing path grants no read.
func RehomeDeclaredOutputPath(ref DeclaredOutputPath, sourceRoot string, roots []string) (string, error) {
	root, valid := canonicalDeclaredRoot(ref.Root)
	if !valid || root != ref.Root || ref.Relative != declaredOutputAbsentPathText && !validRelativePart(ref.Relative) {
		return declaredOutputAbsentPathText, fmt.Errorf(invalidDeclaredOutputPathErrorText)
	}
	allowed := declaredOutputPathUnmatchedState
	for _, stated := range roots {
		if declared, ok := canonicalDeclaredRoot(stated); ok && declared == root {
			allowed = declaredOutputPathMatchedState
			break
		}
	}
	if !allowed || sourceRoot == declaredOutputAbsentPathText || !isAbsoluteSourcePath(sourceRoot) {
		return declaredOutputAbsentPathText, fmt.Errorf(undeclaredOutputPathErrorText)
	}
	relative := root
	if ref.Relative != declaredOutputRootRelativePath && ref.Relative != declaredOutputAbsentPathText {
		relative = path.Join(root, ref.Relative)
	}
	return joinSourcePath(sourceRoot, relative), nil
}

func canonicalDeclaredRoot(stated string) (string, bool) {
	root := strings.ReplaceAll(stated, declaredOutputWindowsSeparatorText, declaredOutputPathSeparatorText)
	if !validRelativePart(root) || root == declaredOutputRootRelativePath {
		return declaredOutputAbsentPathText, declaredOutputPathUnmatchedState
	}
	return root, declaredOutputPathMatchedState
}

func validRelativePart(value string) bool {
	return value != declaredOutputAbsentPathText && !strings.ContainsAny(value, declaredOutputForbiddenPathCharacters) &&
		!strings.HasPrefix(value, declaredOutputPathSeparatorText) && path.Clean(value) == value && !hasParentOrNUL(value)
}

func hasParentOrNUL(value string) bool {
	if strings.ContainsRune(value, declaredOutputPathNULRune) {
		return declaredOutputPathMatchedState
	}
	for _, segment := range strings.FieldsFunc(strings.ReplaceAll(value, declaredOutputWindowsSeparatorText, declaredOutputPathSeparatorText), func(r rune) bool { return r == declaredOutputPathSeparatorRune }) {
		if segment == declaredOutputParentPath {
			return declaredOutputPathMatchedState
		}
	}
	return declaredOutputPathUnmatchedState
}

func isAbsoluteSourcePath(value string) bool {
	return strings.HasPrefix(value, declaredOutputPathSeparatorText) || isWindowsPath(value)
}
