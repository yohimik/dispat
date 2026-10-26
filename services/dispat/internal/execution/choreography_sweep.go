// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

const (
	choreographySweepMissingIdentity  = ""
	choreographySweepNoTasks          = 0
	choreographySweepTaskSeen         = true
	choreographySweepOrderBefore      = -1
	choreographySweepOrderAfter       = 1
	choreographySweepOrderEqual       = 0
	choreographySweepDigestVersion    = 1
	choreographySweepMissingPlanError = "execution: sweep graph lacks a pinned plan or script"
	choreographySweepInvalidTaskError = "execution: invalid or repeated sweep task"
)

// DigestSweepTasks binds a distributed `dispat run` sweep's selected script
// and compiled task identities to the pinned plan digest. The plan digest
// already covers its provider graph and source/config inputs; each reached
// package contributes exactly one task with its resolved placement.
func DigestSweepTasks(planDigest, script string, tasks []ReservationTask) (string, error) {
	if planDigest == choreographySweepMissingIdentity || script == choreographySweepMissingIdentity {
		return choreographySweepMissingIdentity, fmt.Errorf(choreographySweepMissingPlanError)
	}
	type entry struct {
		Task      string    `json:"task"`
		Kind      string    `json:"kind"`
		Placement Placement `json:"placement"`
		Platforms []string  `json:"platforms"`
	}
	entries := make([]entry, choreographySweepNoTasks, len(tasks))
	seen := map[string]bool{}
	for _, task := range tasks {
		if task.Task == choreographySweepMissingIdentity || task.Kind != KindRun || seen[task.Task] {
			return choreographySweepMissingIdentity, fmt.Errorf(choreographySweepInvalidTaskError)
		}
		seen[task.Task] = choreographySweepTaskSeen
		platforms := slices.Clone(task.Platforms)
		slices.Sort(platforms)
		entries = append(entries, entry{Task: task.Task, Kind: task.Kind,
			Placement: task.Placement, Platforms: platforms})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if a.Task < b.Task {
			return choreographySweepOrderBefore
		}
		if a.Task > b.Task {
			return choreographySweepOrderAfter
		}
		return choreographySweepOrderEqual
	})
	data, err := json.Marshal(struct {
		Version    int     `json:"version"`
		PlanDigest string  `json:"planDigest"`
		Script     string  `json:"script"`
		Tasks      []entry `json:"tasks"`
	}{Version: choreographySweepDigestVersion, PlanDigest: planDigest, Script: script, Tasks: entries})
	if err != nil {
		return choreographySweepMissingIdentity, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
