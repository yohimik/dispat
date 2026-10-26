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

// DigestSweepTasks binds a distributed `dispat run` sweep's selected script
// and compiled task identities to the pinned plan digest. The plan digest
// already covers its provider graph and source/config inputs; each reached
// package contributes exactly one task with its resolved placement.
func DigestSweepTasks(planDigest, script string, tasks []ReservationTask) (string, error) {
	if planDigest == "" || script == "" {
		return "", fmt.Errorf("execution: sweep graph lacks a pinned plan or script")
	}
	type entry struct {
		Task      string    `json:"task"`
		Kind      string    `json:"kind"`
		Placement Placement `json:"placement"`
		Platforms []string  `json:"platforms"`
	}
	entries := make([]entry, 0, len(tasks))
	seen := map[string]bool{}
	for _, task := range tasks {
		if task.Task == "" || task.Kind != KindRun || seen[task.Task] {
			return "", fmt.Errorf("execution: invalid or repeated sweep task")
		}
		seen[task.Task] = true
		platforms := slices.Clone(task.Platforms)
		slices.Sort(platforms)
		entries = append(entries, entry{Task: task.Task, Kind: task.Kind,
			Placement: task.Placement, Platforms: platforms})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if a.Task < b.Task {
			return -1
		}
		if a.Task > b.Task {
			return 1
		}
		return 0
	})
	data, err := json.Marshal(struct {
		Version    int     `json:"version"`
		PlanDigest string  `json:"planDigest"`
		Script     string  `json:"script"`
		Tasks      []entry `json:"tasks"`
	}{Version: 1, PlanDigest: planDigest, Script: script, Tasks: entries})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
