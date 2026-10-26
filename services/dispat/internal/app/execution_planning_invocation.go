// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// pinnedSemanticInvocation carries command-line edits and inherited tag
// masks that affect planning. Runtime node addresses, return routes and
// resolved secrets are deliberately absent.
type pinnedSemanticInvocation struct {
	Concurrency    []int                        `json:"concurrency,omitempty"`
	ConcurrencySet bool                         `json:"concurrencySet,omitempty"`
	Polyrepo       bool                         `json:"polyrepo,omitempty"`
	PolyrepoSet    bool                         `json:"polyrepoSet,omitempty"`
	QuietParser    bool                         `json:"quietParser,omitempty"`
	QuietParserSet bool                         `json:"quietParserSet,omitempty"`
	Imports        []execution.PinnedConfigFile `json:"imports,omitempty"`
	IgnoredTags    []string                     `json:"ignoredTags,omitempty"`
	IgnoredByRepo  map[string][]string          `json:"ignoredTagsByRepository,omitempty"`
}

func (a *App) pinnedInvocation() (json.RawMessage, error) {
	invocation := pinnedSemanticInvocation{
		Concurrency:    append([]int(nil), a.planningInvocation.Concurrency...),
		ConcurrencySet: a.planningInvocation.ConcurrencySet,
		Polyrepo:       a.planningInvocation.Polyrepo,
		PolyrepoSet:    a.planningInvocation.PolyrepoSet,
		QuietParser:    a.planningInvocation.QuietParser,
		QuietParserSet: a.planningInvocation.QuietParserSet,
		IgnoredTags:    append([]string(nil), a.ignoreTags...),
		IgnoredByRepo:  a.ignoreTagsByRepository,
	}
	for _, path := range a.planningInvocation.ImportedConfigs {
		name, relative, err := a.pinnedRelativeConfigPath(path)
		if err != nil {
			return nil, err
		}
		invocation.Imports = append(invocation.Imports,
			execution.PinnedConfigFile{Repository: name, Path: filepath.ToSlash(relative)})
	}
	return json.Marshal(invocation)
}

func parsePinnedInvocation(document json.RawMessage) (pinnedSemanticInvocation, error) {
	var invocation pinnedSemanticInvocation
	if len(document) == pinnedPlanningEmptyEntryCount {
		return invocation, fmt.Errorf(pinnedPlanningInvocationAbsentErrorText)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&invocation); err != nil {
		return pinnedSemanticInvocation{}, fmt.Errorf(pinnedPlanningInvocationInvalidErrorFormat, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return pinnedSemanticInvocation{}, fmt.Errorf(pinnedPlanningInvocationTrailingDataErrorText)
	}
	for _, imported := range invocation.Imports {
		path := filepath.Clean(filepath.FromSlash(imported.Path))
		if imported.Path == pinnedPlanningAbsentText || filepath.IsAbs(path) || path == pinnedPlanningParentPath ||
			strings.HasPrefix(path, pinnedPlanningParentPath+string(filepath.Separator)) {
			return pinnedSemanticInvocation{}, fmt.Errorf(pinnedPlanningConfigImportEscapeErrorText)
		}
	}
	return invocation, nil
}
