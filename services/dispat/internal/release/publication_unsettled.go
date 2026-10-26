// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import "fmt"

const publicationUnsettledDiagnosticCode = "E228"
const publicationUnsettledErrorFormat = "%s: publication of %s may still be in flight; its exact destination outcome must be settled before release exclusion ends: %v"
const publicationAttemptStartedState = true

type unsettledPublicationError struct {
	tag   string
	cause error
}

func (e *unsettledPublicationError) Error() string {
	return fmt.Sprintf(publicationUnsettledErrorFormat, publicationUnsettledDiagnosticCode, e.tag, e.cause)
}

func (e *unsettledPublicationError) Unwrap() error { return e.cause }

func (e *unsettledPublicationError) DiagnosticCode() string {
	return publicationUnsettledDiagnosticCode
}

func (tc *taskCtx) unsettledPublication(cause error) error {
	if !tc.localAuthorityAdmitted && tc.OnPublicationUnsettled == nil {
		return cause
	}
	tc.unresolvedEffect = executorUnresolvedEffect
	err := &unsettledPublicationError{tag: tc.rel.TagName(), cause: cause}
	if tc.OnPublicationUnsettled != nil {
		tc.OnPublicationUnsettled(err)
	}
	return err
}
