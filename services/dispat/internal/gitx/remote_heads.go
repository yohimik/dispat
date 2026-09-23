package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// remoteHeadScanner bounds the listing while it arrives. Checking its length
// after buffering the complete reply would let an oversized mailbox exhaust
// memory before the documented ceiling could protect the process.
type remoteHeadScanner struct {
	pending bytes.Buffer
	heads   []RemoteHead
	err     error
}

func (s *remoteHeadScanner) Write(p []byte) (int, error) {
	consumed := len(p)
	for len(p) > 0 && s.err == nil {
		end := bytes.IndexByte(p, '\n')
		length := end
		if end < 0 {
			length = len(p)
		}
		if s.pending.Len()+length > MaxTreeEntryBytes {
			s.err = fmt.Errorf("gitx: a remote ref entry exceeds %d bytes: %w", MaxTreeEntryBytes, ErrTransportLimit)
			break
		}
		s.pending.Write(p[:length])
		if end < 0 {
			break
		}
		s.appendHead(s.pending.String())
		s.pending.Reset()
		p = p[end+1:]
	}
	// Returning the refusal closes the subprocess's output pipe. The command
	// cannot keep streaming into a discarded buffer for the rest of a poll.
	return consumed, s.err
}

func (s *remoteHeadScanner) appendHead(line string) {
	oid, ref, hasSeparator := strings.Cut(line, "\t")
	if !hasSeparator || !fullObjectID(oid) || !strings.HasPrefix(ref, "refs/heads/") {
		s.err = errors.New("gitx: malformed remote ref entry")
		return
	}
	if err := validRefName(ref); err != nil {
		s.err = fmt.Errorf("gitx: malformed remote ref entry: %w", err)
		return
	}
	if len(s.heads) == MaxRemoteHeads {
		s.err = fmt.Errorf("gitx: remote advertises more than %d refs: %w", MaxRemoteHeads, ErrTransportLimit)
		return
	}
	s.heads = append(s.heads, RemoteHead{Name: strings.TrimPrefix(ref, "refs/heads/"), OID: oid})
}
