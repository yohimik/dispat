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
	if l == nil || l.Git == nil || l.held || l.oid != releaseRuntimeAbsentIdentityText ||
		prepared.Run == releaseRuntimeAbsentIdentityText || prepared.Run != l.Run ||
		prepared.Remote == releaseRuntimeAbsentIdentityText || prepared.Remote != l.Remote ||
		!strings.HasPrefix(prepared.LocalTag, gitx.LockAttemptTagPrefix) ||
		prepared.Object == releaseRuntimeAbsentIdentityText {
		return fmt.Errorf(releasePreparedLockIncompleteErrorText)
	}
	local, err := l.Git.TagObject(ctx, prepared.LocalTag)
	if err != nil {
		return fmt.Errorf(releasePreparedLocalLockReadErrorFormat, err)
	}
	if local != prepared.Object {
		return fmt.Errorf(releasePreparedLocalLockMismatchErrorText)
	}
	remote, err := l.readRemoteObjectOnce(ctx)
	if err != nil {
		return fmt.Errorf(releasePreparedRemoteLockReadErrorFormat, err)
	}
	if remote != prepared.Object {
		return fmt.Errorf(releasePreparedRemoteLockMismatchErrorFormat, ErrLockLost)
	}
	l.localTag = prepared.LocalTag
	l.oid = prepared.Object
	l.held = releasePreparedLockHeldState
	return nil
}
