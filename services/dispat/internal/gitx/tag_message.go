// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

const (
	gitTagMessageMaximumBytes             = 16 << 10
	gitTagInvalidNameErrorFormat          = "gitx: invalid local tag name %q: %w"
	gitTagReadErrorFormat                 = "gitx: reading local tag %q: %w"
	gitTagNotAnnotatedErrorFormat         = "gitx: local ref %q names a %s, not an annotated tag"
	gitTagAnnotatedReadErrorFormat        = "gitx: reading annotated tag %q: %w"
	gitTagMissingObjectHeaderErrorFormat  = "gitx: local annotated tag %q has no object header"
	gitTagMissingMessageErrorFormat       = "gitx: local annotated tag %q has no message"
	gitTagWrongIdentityErrorFormat        = "gitx: local annotated tag %q names another tag identity or object type"
	gitTagMaximumBytesExceededErrorFormat = "gitx: annotated tag exceeds %d bytes: %w"
)

// TagMessage reads the message of an exact local annotated tag. A lightweight
// ref or oversized tag cannot serve as a native release-record attestation.
func (c *LocalGitx) TagMessage(ctx context.Context, name string) (string, error) {
	if err := ValidRefName(transportTagRefPrefixText + name); err != nil {
		return transportAbsentObjectText, fmt.Errorf(gitTagInvalidNameErrorFormat, name, err)
	}
	oid, err := c.TagObject(ctx, name)
	if err != nil {
		return transportAbsentObjectText, fmt.Errorf(gitTagReadErrorFormat, name, err)
	}
	kind, err := c.ObjectType(ctx, oid)
	if err != nil {
		return transportAbsentObjectText, err
	}
	if kind != transportTagObjectTypeName {
		return transportAbsentObjectText, fmt.Errorf(gitTagNotAnnotatedErrorFormat, name, kind)
	}
	var content boundedLocalTagWriter
	if _, err := c.runStream(ctx, gitStream{stdout: &content,
		env: []string{gitNoReplaceObjectsEnvironment}}, transportReadObjectCommandName, transportTagObjectTypeName, oid); err != nil {
		return transportAbsentObjectText, fmt.Errorf(gitTagAnnotatedReadErrorFormat, name, err)
	}
	value := content.String()
	if !strings.HasPrefix(value, transportTagTargetHeaderPrefix) {
		return transportAbsentObjectText, fmt.Errorf(gitTagMissingObjectHeaderErrorFormat, name)
	}
	separator := strings.Index(value, transportTagHeaderSeparatorText)
	if separator < transportTagTextStartOffsetBytes {
		return transportAbsentObjectText, fmt.Errorf(gitTagMissingMessageErrorFormat, name)
	}
	var named, commitType bool
	for _, line := range strings.Split(value[:separator], transportObjectLineSeparatorText) {
		switch line {
		case transportTagIdentityHeaderPrefixText + name:
			named = transportTagHeaderMatchedState
		case transportCommitTagTypeHeaderText:
			commitType = transportTagHeaderMatchedState
		}
	}
	if !named || !commitType {
		return transportAbsentObjectText, fmt.Errorf(gitTagWrongIdentityErrorFormat, name)
	}
	return value[separator+transportTagHeaderSeparatorBytes:], nil
}

type boundedLocalTagWriter struct{ bytes.Buffer }

func (w *boundedLocalTagWriter) Write(p []byte) (int, error) {
	if len(p) > gitTagMessageMaximumBytes-w.Len() {
		return transportNoObjectBytesCount, fmt.Errorf(gitTagMaximumBytesExceededErrorFormat,
			gitTagMessageMaximumBytes, ErrTransportLimit)
	}
	return w.Buffer.Write(p)
}
