// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const choreographyNativeInvalidationBrokerError = "execution: native input invalidation has no signed complete graph broker"
const choreographyNativeSnapshotSourceErrorFormat = "execution: package %s has no prepared source for repository %s"
const choreographyNativeMissingGraphRootOID = ""
const choreographyNativeNoSourceSnapshotOID = ""
const choreographyNativeAcceptedSnapshotAbsent = false

// BeginNativeInputWrite blocks compiled claims until this native frame's
// relevant-state check has either signed invalidation or proved no change.
func (c *Coordinator) BeginNativeInputWrite(ctx context.Context) (func(), error) {
	if c == nil || c.GraphRootOID == choreographyNativeMissingGraphRootOID || c.choreographyBroker == nil {
		return func() {}, nil
	}
	return c.choreographyBroker.BeginNativeInputWrite(ctx)
}

// AcceptedPreparedSnapshot reads the source identity from signed accepted
// build finality, including after a cold restart.
func (c *Coordinator) AcceptedPreparedSnapshot(ctx context.Context,
	repository, packageName string) (string, bool, error) {
	if c == nil || c.GraphRootOID == choreographyNativeMissingGraphRootOID || c.choreographyBroker == nil {
		return choreographyNativeNoSourceSnapshotOID, choreographyNativeAcceptedSnapshotAbsent,
			fmt.Errorf(choreographyNativeInvalidationBrokerError)
	}
	return c.choreographyBroker.AcceptedSourceSnapshot(ctx, repository, packageName)
}

// CaptureCurrentPreparedSnapshot records the owner's current working tree,
// including uncommitted native manifest and shared lockfile writes. It uses
// the same snapshot guard and source closure as the accepted build frame.
func (c *Coordinator) CaptureCurrentPreparedSnapshot(ctx context.Context,
	packageName, repository string) (string, error) {
	if c == nil || c.dispatch.Sources == nil {
		return choreographyNativeNoSourceSnapshotOID, fmt.Errorf(choreographyNativeSnapshotSourceErrorFormat, packageName, repository)
	}
	sources := c.dispatch.Sources(packageName)
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		return choreographyNativeNoSourceSnapshotOID, err
	}
	for index, source := range sources {
		if source.Name == repository {
			return commits[index], nil
		}
	}
	return choreographyNativeNoSourceSnapshotOID, fmt.Errorf(choreographyNativeSnapshotSourceErrorFormat, packageName, repository)
}

// CaptureCurrentPreparedSnapshotGuarded is called only from a native frame
// holding Coordinator.Guard. That guard excludes concurrent snapshot capture;
// its repository lane excludes a second writer to this source.
func (c *Coordinator) CaptureCurrentPreparedSnapshotGuarded(ctx context.Context,
	packageName, repository string) (string, error) {
	if c == nil || c.dispatch.Sources == nil || c.dispatch.OpenRepository == nil || c.snapshots == nil {
		return choreographyNativeNoSourceSnapshotOID, fmt.Errorf(choreographyNativeSnapshotSourceErrorFormat, packageName, repository)
	}
	for _, source := range c.dispatch.Sources(packageName) {
		if source.Name == repository {
			return c.snapshots.capture(ctx, c.dispatch.OpenRepository(source.Dir), source, c.Log)
		}
	}
	return choreographyNativeNoSourceSnapshotOID, fmt.Errorf(choreographyNativeSnapshotSourceErrorFormat, packageName, repository)
}

// InvalidatePackagePreparedInput is invoked by the owner's relevant-path
// check while its native publish/file lane is held. The broker derives the
// exact accepted Result descendants and persists the run fence atomically.
func (c *Coordinator) InvalidatePackagePreparedInput(ctx context.Context,
	change ChoreographyPreparedInputChange) error {
	if c == nil || c.GraphRootOID == choreographyNativeMissingGraphRootOID || c.choreographyBroker == nil {
		return fmt.Errorf(choreographyNativeInvalidationBrokerError)
	}
	return c.choreographyBroker.InvalidatePackagePreparedInput(ctx, change)
}
