package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// BackupVersion is the version the kept binary reports, or an error when
// there is no backup or it does not run. It is what --check answers with
// before a rollback, and what a rollback verifies before committing to one.
func BackupVersion(ctx context.Context, exe string) (string, error) {
	backup := BackupPath(exe)
	if _, err := os.Stat(backup); err != nil {
		return "", fmt.Errorf("%w at %s", ErrNoBackup, backup)
	}
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, backup, "--version")
	cmd.Env = append(os.Environ(), "DISPAT_UPDATE_CHECK=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("selfupdate: the backup at %s does not run: %w", backup, err)
	}
	return parseVersionOutput(string(out)), nil
}

// parseVersionOutput picks the version out of what `dispat --version` prints:
// the logo, a blank line, then "dispat <version> (<platform>)". An output
// that does not read that way answers "", which every caller treats as
// "cannot tell", never as a match.
func parseVersionOutput(out string) string {
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "dispat ")
		if !ok {
			continue
		}
		if version, _, ok := strings.Cut(rest, " ("); ok {
			return version
		}
	}
	return ""
}

// Rollback restores the backup over the running binary.
//
// The backup is run before any of that, for the same reason a download is: a
// corrupt file must not be discovered after it is the only dispat left. The
// rotation itself is Restore's, which the download command shares.
func Rollback(ctx context.Context, exe string) (from, to string, err error) {
	if exe == "" {
		if exe, err = Executable(); err != nil {
			return "", "", fmt.Errorf("selfupdate: locating the running binary: %w", err)
		}
	}
	to, err = BackupVersion(ctx, exe)
	if err != nil {
		return "", "", err
	}
	from = parseVersionOutput(runVersion(ctx, exe))
	if err := Restore(exe); err != nil {
		// A rotation that put the backup in place and then failed to keep the
		// replaced binary is a rollback that happened, so it is reported as
		// one, with the leg that did not.
		if errors.Is(err, ErrBackupNotKept) {
			return from, to, err
		}
		return "", "", err
	}
	return from, to, nil
}

// runVersion asks a binary what it is, tolerating everything: the answer is
// only ever used to narrate what a rollback did.
func runVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = append(os.Environ(), "DISPAT_UPDATE_CHECK=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}
