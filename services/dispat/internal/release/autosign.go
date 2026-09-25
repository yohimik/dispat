// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"

	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// Native auto-signing: the sign stage's write of the package's own planned
// version into its own manifests, done by dispat itself (§12.4). It is the
// own-version half of what auto-versioning does without it: a package whose
// space enables autoSign has a version stage that writes dependency ranges and
// replace rules alone, so the two stages never write the same field.
//
// The write reads no other package's manifests, so it needs none of the
// workspace indexes auto-versioning builds, only the scanner.

// autoSign is the sign stage's native step. It runs inside the sign stage
// frame, after beforeSign and before any flow.sign script, and its failure
// fails the stage. Rewriting is format-preserving through pkg/writer, so a
// failure mid-stage plus revertOnFail leaves no half-edited manifest behind.
func (tc *taskCtx) autoSign(ctx context.Context, policy *model.AutoSign) error {
	mans, err := tc.scanOwnManifests(ctx, policy.Manifests)
	if err != nil {
		return err
	}
	for _, m := range mans {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr // interrupted mid-stage: no more rewrites
		}
		// Only the package's own manifests carry its version: a manifest
		// nested inside it (an example, a fixture) has a version story of its
		// own, whatever the scan's scope; see Manifest.IsAtPackageRoot.
		if !m.IsAtPackageRoot() {
			continue
		}
		if !writer.IsSupported(m.Path) && m.Ecosystem != scanner.EcosystemAqua {
			continue // defensive, as in reconcileManifests
		}
		res, err := tc.rewriteManifest(m, tc.resolveOwnVersion(m), nil)
		if err != nil {
			return err
		}
		if res.VersionWritten {
			tc.markManifestsChanged()
			tc.log.Info().Str("manifest", m.Path).Msg("manifest version written")
		}
	}
	return nil
}

// scanOwnManifests scans the package's manifests under the policy's scope. A
// partial scan is reported and the parsed manifests are still written, the way
// auto-versioning treats the same situation; an interrupted one is an
// interruption.
func (tc *taskCtx) scanOwnManifests(ctx context.Context, scope model.ManifestScope) ([]scanner.Manifest, error) {
	mans, err := tc.scanScope(ctx, scope)
	if err == nil {
		return mans, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	tc.log.Warn().Err(err).Msg("auto-signing: some manifests failed to parse")
	return mans, nil
}

// scanScope reads the package's manifests: every one under its folder for the
// all scope, and the ones directly in it otherwise.
func (tc *taskCtx) scanScope(ctx context.Context, scope model.ManifestScope) ([]scanner.Manifest, error) {
	if scope == model.ScopeAll {
		return tc.scan.Scan(ctx, tc.rel.Pkg.Dir)
	}
	return tc.scan.ScanRoot(ctx, tc.rel.Pkg.Dir)
}
