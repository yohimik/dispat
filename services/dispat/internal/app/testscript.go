package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// TestScript computes the plan and executes one top-level script inside one
// package's folder with the package's full DISPAT_* environment — a way to
// try a script under exactly the input a stage would hand it, without
// releasing, tagging or writing anything. The package does not have to be
// changed: an unchanged package's environment simply carries its baseline as
// both the old and the new version, which is what the workspace listing
// reports for it anyway. DISPAT_STAGE is "test:<name>", and $DISPAT_OUTPUT
// works (the exports go nowhere — nothing runs after a test).
func (a *App) TestScript(ctx context.Context, name, pkg string) error {
	cmd, ok := a.cfg.Script(name)
	if !ok {
		err := fmt.Errorf("no top-level script %q (script names are the keys of the scripts map)", name)
		a.log.Error().Err(err).Msg("unknown script")
		return err
	}

	pl, err := a.plan(ctx)
	if err != nil {
		return err
	}
	a.printDiagnostics(pl)
	if pl.Fatal() {
		a.log.Error().Msg("refusing to run: the repository cannot produce a correct plan")
		return errors.New("no correct plan exists")
	}
	rel := pl.Releases[pkg]
	if rel == nil {
		err := fmt.Errorf("unknown package %q (discovered: %s)", pkg, strings.Join(pl.Order, ", "))
		a.log.Error().Err(err).Msg("unknown package")
		return err
	}

	stage := "test:" + name
	log := a.log.With().Str("package", pkg).Str("stage", stage).Logger()
	log.Info().Msg("test script started")
	seq := release.Sequence{Runner: &script.ShellRunner{Shell: a.cfg.Shell}, Dir: rel.Pkg.Dir,
		Stage: stage, Commands: []string{cmd}, Env: release.CommandEnv(pl, pkg, stage, release.WorkspaceEnv(pl, a.log)),
		Log: log, FailFast: true}
	if err := seq.RunMergingOutputs(ctx, rel); err != nil {
		log.Error().Err(err).Msg("test script failed")
		return err
	}
	log.Info().Msg("test script succeeded")
	return nil
}
