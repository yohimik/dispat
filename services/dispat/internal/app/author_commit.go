// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// AuthorCommit delegates an ordinary commit to Git while installing a
// one-invocation commit-msg gate. Git receives every caller argument in its
// original argv element; no shell parses the command line.
func (a *App) AuthorCommit(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	options, err := inspectAuthorArgs(args)
	if err != nil {
		return err
	}
	if options.dryRun {
		a.log.Debug().Msg("delegating dry-run to git; no commit message was validated")
		return a.runAuthorGit(ctx, args, stdout, stderr, "")
	}

	cleanup, err := a.commitCleanupMode(ctx, args)
	if err != nil {
		return err
	}
	hooks, err := a.gitOutput(ctx, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
	if err != nil {
		return fmt.Errorf("resolve git hooks: %w", err)
	}
	tmp, err := os.MkdirTemp("", "dispat-commit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	// The editor marker distinguishes actual editor use from a guessed flag mode.
	editorMarker := filepath.Join(tmp, "edited")
	editor := filepath.Join(tmp, "editor")
	invoker := "#!/bin/sh\n: > " + shellQuote(filepath.ToSlash(editorMarker)) + "\n"
	if original, set := os.LookupEnv("GIT_EDITOR"); set {
		invoker += "GIT_EDITOR=" + shellQuote(original) + "; export GIT_EDITOR\n"
	} else {
		invoker += "unset GIT_EDITOR\n"
	}
	// Git editor settings are trusted shell commands. Use the same sh -c argument
	// boundary as Git; never insert the message or a caller path into that source.
	invoker += `editor=$(git var GIT_EDITOR) || exit $?
exec /bin/sh -c "$editor \"\$@\"" "$editor" "$@"
`
	if err := os.WriteFile(editor, []byte(invoker), 0o700); err != nil {
		return err
	}
	proxy := filepath.Join(tmp, "hooks")
	if err := copyHooks(strings.TrimSpace(hooks), proxy); err != nil {
		return err
	}
	parserFile := filepath.Join(tmp, "parser.json")
	encoded, err := json.Marshal(a.cfg.ResolvedParser)
	if err != nil {
		return err
	}
	if err := os.WriteFile(parserFile, encoded, 0o600); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	original := findHook(strings.TrimSpace(hooks), "commit-msg", runtime.GOOS == "windows")
	script := "#!/bin/sh\nset -e\n"
	if original != "" {
		script += shellQuote(filepath.ToSlash(original)) + " \"$@\"\n"
	}
	script += "DISPAT_INTERNAL_COMMIT_VALIDATE=1 DISPAT_COMMIT_PARSER=" + shellQuote(filepath.ToSlash(parserFile)) +
		" DISPAT_COMMIT_EDITED=" + shellQuote(filepath.ToSlash(editorMarker)) +
		" DISPAT_COMMIT_LOG_FORMAT=" + shellQuote(a.cfg.LogFormat) +
		" DISPAT_COMMIT_LOG_LEVEL=" + shellQuote(a.cfg.LogLevel) +
		" DISPAT_COMMIT_CLEANUP=" + shellQuote(cleanup) + " " + shellQuote(filepath.ToSlash(exe)) + " \"$1\"\n"
	if err := os.WriteFile(filepath.Join(proxy, "commit-msg"), []byte(script), 0o700); err != nil {
		return err
	}
	a.log.Debug().Str("cleanup", cleanup).Msg("validating git commit message")
	return a.runAuthorGit(ctx, args, stdout, stderr, proxy)
}

func (a *App) commitCleanupMode(ctx context.Context, args []string) (string, error) {
	options, err := inspectAuthorArgs(args)
	if err != nil {
		return "", err
	}
	mode := options.cleanup
	if mode == "" {
		configured, err := a.gitOutput(ctx, "config", "--get", "commit.cleanup")
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				return "", fmt.Errorf("read commit.cleanup: %w", err)
			}
		}
		mode = strings.TrimSpace(configured)
	}
	if mode == "" {
		mode = "default"
	}
	switch mode {
	case "default", "strip", "whitespace", "verbatim", "scissors":
	default:
		return "", fmt.Errorf("unsupported git cleanup mode %q", mode)
	}
	// stripspace cannot infer the comment character selected by Git's editor
	// when core.commentChar=auto. Refuse that configuration before Git mutates
	// the index rather than validate a different message.
	comment, err := a.gitOutput(ctx, "config", "--get", "core.commentChar")
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return "", fmt.Errorf("read core.commentChar: %w", err)
		}
	}
	if strings.TrimSpace(comment) == "auto" && mode != "verbatim" && mode != "whitespace" {
		return "", errors.New("validated commits require an explicit core.commentChar for comment cleanup; auto is not supported")
	}
	return mode, nil
}

// Git accepts abbreviated and clustered flags. Resolve the grammar before
// invoking it so a bypass switch cannot hide in a cluster or in an abbreviation.
// Unknown long forms are refused; Git still validates values and combinations.
type authorArgs struct {
	cleanup  string
	dryRun   bool
	boundary int
}

func inspectAuthorArgs(args []string) (authorArgs, error) {
	o := authorArgs{boundary: len(args)}
	valueFlags := map[string]bool{"message": true, "file": true, "reuse-message": true, "reedit-message": true,
		"author": true, "date": true, "template": true, "cleanup": true, "trailer": true,
		"fixup": true, "squash": true, "pathspec-from-file": true}
	plainFlags := map[string]bool{"all": true, "patch": true, "signoff": true, "no-signoff": true,
		"edit": true, "no-edit": true, "amend": true, "reset-author": true, "allow-empty": true, "allow-empty-message": true,
		"dry-run": true, "short": true, "branch": true, "no-branch": true, "long": true, "null": true,
		"verbose": true, "no-verbose": true, "quiet": true, "status": true, "no-status": true,
		"include": true, "only": true, "no-post-rewrite": true, "pathspec-file-nul": true,
		"gpg-sign": true, "no-gpg-sign": true, "untracked-files": true, "porcelain": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			o.boundary = i
			break
		}
		if strings.HasPrefix(arg, "--") {
			name, value, inline := strings.Cut(arg[2:], "=")
			if name == "no-verify" {
				return o, errors.New("--no-verify/-n bypasses commit-msg validation and is not allowed")
			}
			if valueFlags[name] {
				if !inline {
					i++
					if i >= len(args) {
						return o, fmt.Errorf("--%s requires a value", name)
					}
					value = args[i]
				}
				if name == "cleanup" {
					o.cleanup = value
				}
			} else if !plainFlags[name] {
				return o, fmt.Errorf("unsupported authoring flag --%s; spell Git options in full", name)
			}
			if name == "dry-run" || name == "short" || name == "porcelain" || name == "long" {
				o.dryRun = true
			}
			continue
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		for j := 1; j < len(arg); j++ {
			flag := arg[j]
			if flag == 'n' {
				return o, errors.New("--no-verify/-n bypasses commit-msg validation and is not allowed")
			}
			if strings.ContainsRune("mFCct", rune(flag)) {
				if j == len(arg)-1 {
					i++
					if i >= len(args) {
						return o, fmt.Errorf("-%c requires a value", flag)
					}
				}
				break
			}
			if flag == 'S' || flag == 'u' {
				break
			} // optional attached argument
			if !strings.ContainsRune("apesvioqz", rune(flag)) {
				return o, fmt.Errorf("unsupported authoring flag -%c", flag)
			}
		}
	}
	return o, nil
}

func (a *App) runAuthorGit(ctx context.Context, args []string, stdout, stderr io.Writer, hooks string) error {
	base := []string{"-C", a.root}
	if hooks != "" {
		base = append(base, "-c", "core.hooksPath="+hooks)
	}
	base = append(base, "commit")
	if hooks != "" {
		options, err := inspectAuthorArgs(args)
		if err != nil {
			return err
		}
		at := options.boundary
		base = append(base, args[:at]...)
		base = append(base, "--cleanup=verbatim")
		base = append(base, args[at:]...)
	} else {
		base = append(base, args...)
	}
	cmd := exec.CommandContext(ctx, "git", base...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	cmd.Env = os.Environ()
	if hooks != "" {
		cmd.Env = append(cmd.Env, "GIT_EDITOR="+shellQuote(filepath.ToSlash(filepath.Join(filepath.Dir(hooks), "editor"))))
	}
	cmd.WaitDelay = 10 * time.Second
	script.SetProcessGroupForce(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

func (a *App) gitOutput(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", a.root}, args...)...)
	cmd.WaitDelay = time.Second
	script.SetProcessGroup(cmd)
	var out limitedOutput
	cmd.Stdout = &out
	err := cmd.Run()
	if out.over {
		return "", errors.New("git configuration output exceeds 64 KiB")
	}
	return out.String(), err
}

type limitedOutput struct {
	bytes []byte
	over  bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	const limit = 64 << 10
	written := len(p)
	before := len(w.bytes)
	if len(w.bytes) < limit {
		n := limit - len(w.bytes)
		if n > len(p) {
			n = len(p)
		}
		w.bytes = append(w.bytes, p[:n]...)
	}
	if before+len(p) > limit {
		w.over = true
	}
	return written, nil
}
func (w *limitedOutput) String() string { return string(w.bytes) }

func copyHooks(source, target string) error {
	return copyHooksForPlatform(source, target, runtime.GOOS == "windows")
}

func copyHooksForPlatform(source, target string, windows bool) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(source, entry.Name())
		if !hookExecutable(path, windows) {
			continue
		}
		name := entry.Name()
		fallback := windows && strings.HasSuffix(strings.ToLower(name), ".exe")
		if fallback {
			name = name[:len(name)-len(".exe")]
			if hookExecutable(filepath.Join(source, name), true) {
				continue
			}
		}
		if name == "commit-msg" {
			continue
		}
		wrapper := "#!/bin/sh\nexec " + shellQuote(filepath.ToSlash(path)) + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(target, name), []byte(wrapper), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func executable(path string) bool {
	return hookExecutable(path, runtime.GOOS == "windows")
}

// hookExecutable mirrors the platform part of Git's hook discovery. Unix
// requires an executable regular file. Git for Windows treats regular files
// as executable because its access(X_OK) compatibility layer cannot rely on
// Unix mode bits.
func hookExecutable(path string, windows bool) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && (windows || info.Mode().Perm()&0o111 != 0)
}

// findHook applies Git for Windows' executable-extension fallback while
// preferring the extensionless spelling on every platform.
func findHook(dir, name string, windows bool) string {
	base := filepath.Join(dir, name)
	if hookExecutable(base, windows) {
		return base
	}
	if windows {
		candidate := base + ".exe"
		if hookExecutable(candidate, true) {
			return candidate
		}
	}
	return ""
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
