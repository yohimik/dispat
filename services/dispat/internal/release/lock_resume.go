package release

import (
	"context"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// AdoptPrepared restores a lock after the first-node journal's exclusive OS
// lock has been taken. It never pushes or steals a remote lock. The caller
// must separately authenticate the retained run bootstrap and replay its
// ledger before enabling any new effect.
func (l *Lock) AdoptPrepared(ctx context.Context, prepared PreparedLockIdentity) error {
	if l == nil || l.Git == nil || l.held || l.oid != "" ||
		prepared.Run == "" || prepared.Run != l.Run ||
		prepared.Remote == "" || prepared.Remote != l.Remote ||
		!strings.HasPrefix(prepared.LocalTag, gitx.LockAttemptTagPrefix) ||
		prepared.Object == "" {
		return fmt.Errorf("release: incomplete prepared lock identity")
	}
	local, err := l.Git.TagObject(ctx, prepared.LocalTag)
	if err != nil || local != prepared.Object {
		return fmt.Errorf("release: local prepared lock identity differs from journal: %w", err)
	}
	remote, err := l.readRemoteObjectOnce(ctx)
	if err != nil {
		return fmt.Errorf("release: prepared remote lock could not be verified: %w", err)
	}
	if remote != prepared.Object {
		return fmt.Errorf("%w: prepared remote lock differs from journal", ErrLockLost)
	}
	l.localTag = prepared.LocalTag
	l.oid = prepared.Object
	l.held = true
	return nil
}
