package selfupdate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
// It rotates rather than moves: the binary being replaced becomes the new
// backup, so a rollback is itself reversible and no version is ever lost. The
// backup is run before any of that, for the same reason a download is: a
// corrupt file must not be discovered after it is the only dispat left.
func Rollback(ctx context.Context, exe string) (from, to string, err error) {
	if exe == "" {
		if exe, err = Executable(); err != nil {
			return "", "", fmt.Errorf("selfupdate: locating the running binary: %w", err)
		}
	}
	backup := BackupPath(exe)
	to, err = BackupVersion(ctx, exe)
	if err != nil {
		return "", "", err
	}
	from = parseVersionOutput(runVersion(ctx, exe))

	dir := filepath.Dir(exe)
	parked, err := os.CreateTemp(dir, tempPattern(exe, "rollback"))
	if err != nil {
		return "", "", fmt.Errorf("selfupdate: %s is not writable (%w); "+
			"re-run with the rights to replace %s", dir, err, exe)
	}
	parkedName := parked.Name()
	parked.Close()
	// CreateTemp made the file so the name is ours; the rename needs it gone
	// on the platforms that will not rename onto an existing file.
	if err := os.Remove(parkedName); err != nil {
		return "", "", fmt.Errorf("selfupdate: %s: %w", parkedName, err)
	}

	if err := os.Rename(exe, parkedName); err != nil {
		return "", "", fmt.Errorf("selfupdate: moving %s aside: %w", exe, err)
	}
	if err := os.Rename(backup, exe); err != nil {
		if back := os.Rename(parkedName, exe); back != nil {
			return "", "", fmt.Errorf("selfupdate: restoring %s failed (%w) and putting the current binary "+
				"back failed too (%v): it is at %s", exe, err, back, parkedName)
		}
		return "", "", fmt.Errorf("selfupdate: restoring %s: %w", exe, err)
	}
	// The third leg of the rotate, and a plain rename rather than Replace:
	// Replace puts a file in exe's place, which is what the leg above already
	// did, and running it here would swap the two straight back.
	if err := os.Rename(parkedName, backup); err != nil {
		// exe is the rolled-back binary either way, which is what was asked
		// for; only the new backup is missing. Say so rather than report a
		// rollback that did happen as a failure.
		return from, to, fmt.Errorf("selfupdate: rolled back to %s, but keeping %s as the new backup failed: %w",
			to, from, err)
	}
	now := time.Now()
	_ = os.Chtimes(backup, now, now)
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
