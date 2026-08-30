package config

// Finding the file, when nobody named one.
//
// A command run from inside a sub-folder of a project should load the
// project's configuration, and the folder the configuration sits in is the
// project's root. That is an ascent: the starting directory, then its parent,
// then its parent's, up to the filesystem root, stopping at the first file
// that says it is the root.
//
// What a found file says is the caller's to decide, because only the caller
// knows its own language. A sub-folder may carry a configuration file of its
// own — an override layer — and telling one of those apart from a root is the
// whole difficulty. The Resolver is the four questions the ascent has to ask:
// which names count, which of them a folder offers, what a parsed file turns
// out to be, and whether a root claims the folder a weaker candidate was found
// in.

import (
	"context"
	"os"
	"path/filepath"
)

// Class is what a file found during the ascent turns out to be.
type Class int

const (
	// ClassFallback is a file that declares neither: a leaf folder's override
	// file, or a root missing what would mark it. It is remembered as the
	// weakest answer and the ascent goes on.
	ClassFallback Class = iota
	// ClassCandidate is a file that could be a root and could be a layer — it
	// declares what a root declares, but so does a sub-folder's file. It is
	// remembered and the ascent goes on.
	ClassCandidate
	// ClassRoot is a file that can only be a root. The ascent stops there.
	ClassRoot
)

// String names the class as the resolve events spell it.
func (c Class) String() string {
	switch c {
	case ClassRoot:
		return "root"
	case ClassCandidate:
		return "candidate"
	}
	return "fallback"
}

// Resolver is the caller's half of the ascent.
//
// The zero value resolves nothing: Names and Candidates are what a directory
// is asked, and a Resolver without them finds no file anywhere. Classify and
// Owns may be nil, which makes every file found a root — the ascent of a
// language with no override layers.
type Resolver struct {
	// Names are the file names looked for, in precedence order. They are what
	// a failure lists, so they read as the answer to "what should I have
	// written".
	Names []string

	// Candidates returns the names of Names that dir offers, in precedence
	// order, or none. A directory contributes its first candidate alone: the
	// precedence within one folder is decided there rather than by the ascent.
	//
	// It is a function rather than a directory listing so that a folder can
	// exclude a name it holds — a folder carrying two config files saying
	// which one is real — and so that the probe is the same one the caller's
	// other loaders use.
	Candidates func(dir string) ([]string, error)

	// Classify places a parsed file. A nil Classify makes every file a root.
	Classify func(root map[string]any) Class

	// Owns reports whether the root config parsed into root, sitting in
	// rootDir, claims the folder dir — which is what tells a sub-folder's own
	// file apart from a project of its own. A nil Owns claims nothing, so a
	// remembered candidate always wins over an ancestor root.
	Owns func(root map[string]any, rootDir, dir string) bool
}

// Resolve returns the path of the configuration file to load and the directory
// it establishes as the root.
//
// The ascent starts at dir and walks up to the filesystem root. A file that
// Classify calls a root ends it; a file that cannot be read ends it too,
// because a broken root config must fail where configuration is loaded rather
// than be silently stepped over. A candidate or a fallback is remembered and
// the walk goes on, and a remembered candidate is only displaced by a root
// that Owns the folder it was found in.
//
// When no file on the way up is a root, the nearest remembered one is returned
// anyway: whatever the caller's own validation says about it is the real
// mistake, and it is a better message than this one could be. When nothing is
// found at all, the error is a *NoConfigError naming every candidate tried.
func (l *Loader) Resolve(ctx context.Context, dir string, r Resolver) (path, root string, err error) {
	l = l.loader()
	log := l.logger(ctx)
	var candidate, candidateRoot string // could be a root, could be a layer
	var fallback, fallbackRoot string   // declares nothing either way
	try := func(at string) (string, string, error) {
		names, err := r.candidates(at)
		if err != nil {
			return "", "", err
		}
		if len(names) == 0 {
			return "", "", nil
		}
		p := filepath.Join(at, names[0])
		// One parse answers both questions this file is asked, so a candidate
		// on the way up is read once however far the ascent goes.
		t, readErr := l.ReadTree(ctx, p)
		if readErr != nil {
			// A file that cannot be read is broken rather than skippable:
			// loading is where a broken config fails loudly, and stepping over
			// it to use a parent's file would hide the breakage.
			return p, at, nil
		}
		class := r.classify(t.Root)
		if log.Enabled(LevelTrace) {
			log.Log(LevelTrace, EventResolveStep, Str("dir", at), Str("candidate", p),
				Str("class", class.String()))
		}
		switch class {
		case ClassRoot:
			// A candidate below is a sub-folder's file when this root claims
			// its folder, and a project of its own when it does not.
			if candidate != "" && !r.owns(t.Root, at, candidateRoot) {
				return candidate, candidateRoot, nil
			}
			return p, at, nil
		case ClassCandidate:
			if candidate == "" {
				candidate, candidateRoot = p, at
			}
		default:
			if fallback == "" {
				fallback, fallbackRoot = p, at
			}
		}
		return "", "", nil
	}
	found := func(p, rt string) (string, string, error) {
		if log.Enabled(LevelDebug) {
			log.Log(LevelDebug, EventResolveDone, Str("path", p), Str("root", rt))
		}
		return p, rt, nil
	}
	for _, at := range ascent(dir) {
		p, rt, err := try(at)
		if err != nil {
			return "", "", err
		}
		if p != "" {
			return found(p, rt)
		}
	}
	if candidate != "" {
		return found(candidate, candidateRoot)
	}
	if fallback != "" {
		return found(fallback, fallbackRoot)
	}
	return "", "", &NoConfigError{Dir: dir, Names: r.Names}
}

// ascent is the directories to try, in order: the one asked for as it was
// written, and then its parents. Beyond the start the walk is absolute, which
// makes it well-defined wherever a relative start pointed.
func ascent(dir string) []string {
	dirs := []string{dir}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dirs
	}
	for at := filepath.Dir(abs); ; at = filepath.Dir(at) {
		dirs = append(dirs, at)
		if at == filepath.Dir(at) { // filesystem root
			break
		}
	}
	return dirs
}

// candidates asks the resolver which files a folder offers, falling back to
// the plain "the name exists" probe when the caller gave none.
func (r Resolver) candidates(dir string) ([]string, error) {
	if r.Candidates != nil {
		return r.Candidates(dir)
	}
	var present []string
	for _, name := range r.Names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			present = append(present, name)
		}
	}
	return present, nil
}

func (r Resolver) classify(root map[string]any) Class {
	if r.Classify == nil {
		return ClassRoot
	}
	return r.Classify(root)
}

func (r Resolver) owns(root map[string]any, rootDir, dir string) bool {
	if r.Owns == nil {
		return false
	}
	return r.Owns(root, rootDir, dir)
}

// MarkerClassify is the Classify most languages want: a file is a root when it
// declares any of rootKeys, a candidate when it declares any of
// candidateKeys, and a fallback otherwise.
//
// A key holding null is not a declaration, which is the rule the rest of the
// language follows too: a map spelled out as empty says no more than an absent
// one. Keys are matched case-insensitively, because the tree holds the keys
// the file wrote while the probe asking about it is spelled in the language's
// own lower case.
func MarkerClassify(rootKeys, candidateKeys []string) func(map[string]any) Class {
	return func(root map[string]any) Class {
		for _, key := range rootKeys {
			if IsSet(root, key) {
				return ClassRoot
			}
		}
		for _, key := range candidateKeys {
			if IsSet(root, key) {
				return ClassCandidate
			}
		}
		return ClassFallback
	}
}

// FolderOwner is the Owns for a language whose root config declares its
// sub-projects as an object of entries, each naming the folder or folders it
// covers: collectionKey is the object, pathKey the entry's folder key, whose
// value is one path or a list of them.
//
// Folders are compared by identity rather than by name, so a symlinked or
// case-insensitively-spelled path still matches itself.
func FolderOwner(collectionKey, pathKey string) func(map[string]any, string, string) bool {
	return func(root map[string]any, rootDir, dir string) bool {
		target, err := os.Stat(dir)
		if err != nil {
			return false
		}
		_, raw, _ := LookupFold(root, collectionKey)
		entries, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		for _, raw := range entries {
			fields, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			_, rawPath, found := LookupFold(fields, pathKey)
			if !found {
				continue
			}
			paths, ok := scalarOrList(rawPath)
			if !ok {
				continue
			}
			for _, p := range paths {
				if p == "" {
					continue
				}
				info, err := os.Stat(filepath.Join(rootDir, filepath.FromSlash(p)))
				if err == nil && os.SameFile(info, target) {
					return true
				}
			}
		}
		return false
	}
}

// scalarOrList reads the recurring "one name or a list of names" shape out of
// a parsed tree, before any decode has typed it.
func scalarOrList(v any) ([]string, bool) {
	switch x := v.(type) {
	case string:
		return []string{x}, true
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}
