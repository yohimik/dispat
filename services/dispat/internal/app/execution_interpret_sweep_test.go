// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

const (
	interpretedSweepTestBase      = "node test"
	interpretedSweepTestChanged   = "node other"
	interpretedSweepTestArg       = "--check"
	interpretedSweepTestSpaceArg  = "two words"
	interpretedSweepTestQuoteArg  = "it's fine"
	interpretedSweepTestEmptyArg  = ""
	interpretedSweepTestBadSuffix = " --check "
)

func TestInterpretedSweepRecoversOnlyCanonicalSignedArgumentSuffix(t *testing.T) {
	for _, noArgs := range [][]string{nil, {}} {
		encoded, err := json.Marshal(noArgs)
		require.NoError(t, err)
		hash := sha256.Sum256(encoded)
		got, err := recoverInterpretedSweepArgs(interpretedSweepTestBase,
			interpretedSweepTestBase, hex.EncodeToString(hash[:]))
		require.NoError(t, err)
		require.Equal(t, noArgs, got)
	}
	args := []string{interpretedSweepTestArg, interpretedSweepTestSpaceArg,
		interpretedSweepTestQuoteArg, interpretedSweepTestEmptyArg}
	document, err := json.Marshal(args)
	require.NoError(t, err)
	hash := sha256.Sum256(document)
	got, err := recoverInterpretedSweepArgs(interpretedSweepTestBase,
		script.AppendArgs(interpretedSweepTestBase, args), hex.EncodeToString(hash[:]))
	require.NoError(t, err)
	require.Equal(t, args, got)
	_, err = recoverInterpretedSweepArgs(interpretedSweepTestChanged,
		script.AppendArgs(interpretedSweepTestBase, args), hex.EncodeToString(hash[:]))
	require.Error(t, err)
	_, err = recoverInterpretedSweepArgs(interpretedSweepTestBase,
		interpretedSweepTestBase+interpretedSweepTestBadSuffix, hex.EncodeToString(hash[:]))
	require.Error(t, err)
	_, err = recoverInterpretedSweepArgs(interpretedSweepTestBase,
		script.AppendArgs(interpretedSweepTestBase, args), interpretedSweepTestChanged)
	require.Error(t, err)
}
