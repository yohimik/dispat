// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// Publish/tag reconciliation (CCME SPEC.md §19.4).
//
// A publication and its release record are two writes to two systems, so a run
// can stop between them. The next run plans the same version, and a registry
// that keeps every version it accepted refuses it. Without a rule, every run
// after that fails identically. The rule is to ask the destination whether it
// holds this run's artefact under that version: adopt it when it does, refuse
// it when it holds something else. Adopting without that check would enrol an
// artefact nobody in this repository produced.
//
// A publish command is a shell command, and dispat cannot read why it failed.
// The verifier answers that too: a destination that does not hold the version
// means the command failed for a reason of its own, which stays an ordinary
// publish failure.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// IntegrityOutput is the script output naming a package's artefact identity
// in the destination's own spelling: npm's `sha512-…` integrity, an image
// digest. A build or publish script exports it as INTEGRITY=<value>. The
// built-in npm check compares it with the registry's integrity field, and a
// receipt carries it. A pending-receipt retry skips publish, so the value
// must come from an earlier repeatable stage to verify that retry.
const IntegrityOutput = "INTEGRITY"

// adoptStage is what DISPAT_STAGE carries for the verify scripts.
const adoptStage = "adoptPublished"

// verifyAbsentExit is the exit code a verify script answers with when the
// destination does not hold the version.
const verifyAbsentExit = 3

// adoptionVerdict is what the verifier made of a refused publication.
type adoptionVerdict uint8

const (
	// verdictUnasked: no verifier applies, or it could not reach the
	// destination. The publish failure stands as it is.
	verdictUnasked adoptionVerdict = iota
	// verdictAbsent: the destination does not hold the version, so the
	// publish failed for a reason of its own.
	verdictAbsent
	// verdictMatch: the destination holds this run's artefact.
	verdictMatch
	// verdictMismatch: the destination holds the version, and it is not this
	// run's artefact or its identity cannot be established.
	verdictMismatch
)

// adoptionRefused is the E198 a refused adoption fails its package with.
type adoptionRefused struct {
	tag    string
	reason string
}

func (e *adoptionRefused) Error() string {
	return fmt.Sprintf("%s: the destination already holds %s and %s", plan.CodeAdoptionRefused, e.tag, e.reason)
}

// DiagnosticCode is the code the failure event carries.
func (e *adoptionRefused) DiagnosticCode() string { return plan.CodeAdoptionRefused }

// reconcilePublication decides a failed publish command under §19.4, and
// answers what the task fails with: nothing when the version is adopted, an
// E198 when it is refused, and the original failure otherwise.
func (tc *taskCtx) reconcilePublication(ctx context.Context, what string, publishErr error) (string, error) {
	if ctx.Err() != nil || tc.rel.Pkg.Space.AdoptPublished.IsDisabled || isOutcomeUnknown(publishErr) {
		return what, publishErr
	}
	verdict, evidence := tc.verifyPublished(ctx)
	switch verdict {
	case verdictMatch:
		tc.log.Warn().Str("code", plan.CodeAdopted).Str("tag", tc.rel.TagName()).Str("evidence", evidence).
			Msg("the destination already held this version as this run's artefact; adopted it as published")
		return "", nil
	case verdictMismatch:
		tc.refusePublications()
		return "publish refused: the destination holds this version as another artefact",
			&adoptionRefused{tag: tc.rel.TagName(), reason: evidence}
	default:
		return what, publishErr
	}
}

// isOutcomeUnknown reports a publication whose node never said what became of
// it (§28.6). Its reconciliation belongs to the operator's recovery order, not
// to the run that lost track of it.
func isOutcomeUnknown(err error) bool {
	var diagnostic interface{ DiagnosticCode() string }
	return errors.As(err, &diagnostic) && diagnostic.DiagnosticCode() == "E228"
}

// verifyPublished asks the configured verify scripts, or the built-in npm
// check when none is configured, whether the destination holds this run's
// artefact. evidence is what a person reading the line needs to check it.
func (tc *taskCtx) verifyPublished(ctx context.Context) (adoptionVerdict, string) {
	if scripts := tc.rel.Pkg.Space.AdoptPublished.VerifyScript; len(scripts) > 0 {
		return tc.runVerifyScripts(ctx, scripts)
	}
	return tc.verifyNpmIntegrity(ctx)
}

// runVerifyScripts runs the space's verify scripts with the environment the
// publish had, and reads their exit status.
func (tc *taskCtx) runVerifyScripts(ctx context.Context, scripts []string) (adoptionVerdict, string) {
	err := tc.sequence(ctx, adoptStage, scripts, true).Run(ctx)
	if err == nil {
		return verdictMatch, "the verify script matched it"
	}
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) && exit.ExitCode() == verifyAbsentExit {
		return verdictAbsent, ""
	}
	if ctx.Err() != nil {
		return verdictUnasked, ""
	}
	return verdictMismatch, "the verify script did not match it: " + err.Error()
}

// verifyNpmIntegrity is the built-in check: when the package's scripts
// exported a Subresource Integrity value and the folder is an npm package, it
// reads `dist.integrity` of the planned version from the registry npm itself
// is configured for, and compares the two.
func (tc *taskCtx) verifyNpmIntegrity(ctx context.Context) (adoptionVerdict, string) {
	integrity, isExported := tc.rel.Output(IntegrityOutput)
	integrity = strings.TrimSpace(integrity)
	if !isExported || !isSubresourceIntegrity(integrity) {
		return verdictUnasked, ""
	}
	name, err := readNpmName(tc.rel.Pkg.Dir)
	if err != nil {
		tc.log.Debug().Err(err).Msg("adoptPublished: no npm package name, the built-in check does not apply")
		return verdictUnasked, ""
	}
	spec := name + "@" + tc.rel.Next.String()
	var stdout, stderr bytes.Buffer
	err = tc.Runner.Run(ctx, tc.rel.Pkg.Dir, "npm view "+quoteShell(spec)+" dist.integrity",
		tc.env(adoptStage), &stdout, &stderr)
	if err != nil {
		if strings.Contains(stderr.String(), "E404") {
			return verdictAbsent, ""
		}
		tc.log.Warn().Err(err).Str("package", spec).
			Msg("adoptPublished: the registry could not be asked whether it holds this version; the publish failure stands")
		return verdictUnasked, ""
	}
	held := strings.Trim(strings.TrimSpace(stdout.String()), `"'`)
	switch held {
	case "":
		return verdictMismatch, "the registry returned this version without a verifiable dist.integrity"
	case integrity:
		return verdictMatch, "the registry's integrity of " + spec + " is " + held
	}
	return verdictMismatch, "its integrity is " + held + ", this run's artefact is " + integrity
}

// isSubresourceIntegrity reports whether a value has the shape npm records an
// integrity in: an algorithm, a dash, and a digest.
func isSubresourceIntegrity(value string) bool {
	for _, algorithm := range []string{"sha512-", "sha384-", "sha256-", "sha1-"} {
		if strings.HasPrefix(value, algorithm) && len(value) > len(algorithm) {
			return true
		}
	}
	return false
}

// readNpmName reads the package name a folder's package.json declares.
func readNpmName(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", err
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if manifest.Name == "" {
		return "", errors.New("package.json declares no name")
	}
	return manifest.Name, nil
}

// quoteShell quotes one word for a POSIX shell.
func quoteShell(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// refusePublications records, under mu, that this run met an artefact it may
// not adopt. No publication starts after it (§19.4: raise E198 and stop).
func (tc *taskCtx) refusePublications() {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.isPublicationRefused = true
}
