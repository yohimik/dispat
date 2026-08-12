package config

// Change-scope ignore rules: the patterns that keep some of a package's own
// files from counting as changes to it.
//
// Every level says the same thing two ways — an `ignore` list in the config
// and a .dispatignore file in the folder — and the two are read one after the
// other, the file last, because the file sits where the folder is and is
// therefore the nearer statement about it. The levels themselves concatenate
// into a chain the planner walks; nothing here replaces anything.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/ignore"
)

// DispatignoreName is the per-folder change-scope ignore file: one pattern
// per line, matched against the paths of the changed files under the folder
// it sits in. It is the file form of the `ignore` key, and deliberately not
// .dispatexclude, which decides something else entirely — what is a package.
const DispatignoreName = ".dispatignore"

// ignoreLayer compiles one level's patterns: the config list first, then the
// folder's own .dispatignore, both relative to dir. A level with nothing to
// say returns a layer with nil rules, which the chain builder drops.
//
// dir is an absolute filesystem path; the layer keeps it slash-separated,
// which is the form changed-file paths arrive in.
func ignoreLayer(dir string, patterns []string) (ignore.Layer, error) {
	filePatterns, err := readIgnoreFile(dir)
	if err != nil {
		return ignore.Layer{}, err
	}
	if len(patterns) == 0 && len(filePatterns) == 0 {
		return ignore.Layer{}, nil
	}
	all := make([]string, 0, len(patterns)+len(filePatterns))
	all = append(all, patterns...)
	all = append(all, filePatterns...)
	rules, err := ignore.Compile(all)
	if err != nil {
		return ignore.Layer{}, err
	}
	return ignore.Layer{Dir: filepath.ToSlash(dir), Rules: rules}, nil
}

// readIgnoreFile reads a folder's .dispatignore. An absent file is no
// patterns rather than an error: most folders have nothing to exclude.
func readIgnoreFile(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, DispatignoreName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", DispatignoreName, err)
	}
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), nil
}

// ignoreChain assembles the layers that apply to one package, weakest first.
// Layers with nothing to say are dropped, so the common case is an empty
// chain and the planner's question costs one length check.
//
// The outer layers are shared by every package that folds through them: the
// caller builds them once, and the chain holds them by value with the rules
// behind a pointer.
func ignoreChain(outer ignore.Chain, pkgLayer ignore.Layer) ignore.Chain {
	if pkgLayer.Rules == nil {
		return outer
	}
	chain := make(ignore.Chain, 0, len(outer)+1)
	chain = append(chain, outer...)
	return append(chain, pkgLayer)
}

// appendLayer adds a layer to a chain unless it has nothing to say.
func appendLayer(chain ignore.Chain, l ignore.Layer) ignore.Chain {
	if l.Rules == nil {
		return chain
	}
	return append(chain, l)
}

// packageIgnore builds one package's chain: the levels above it, then its own
// patterns and the .dispatignore in its folder.
func packageIgnore(outer ignore.Chain, dir string, patterns []string) (ignore.Chain, error) {
	layer, err := ignoreLayer(dir, patterns)
	if err != nil {
		return nil, err
	}
	return ignoreChain(outer, layer), nil
}
