package plan

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestReleaseReceiptUsesOnlyAnAnnotatedCanonicalTag(t *testing.T) {
	message, err := RenderReleaseTagMessage("cli@1.0.0", map[string]string{
		"core": "core@0.1.0", "engine": "",
	})
	require.NoError(t, err)
	tag := gitx.Tag{Name: "cli@1.0.0", Annotated: true,
		Subject: message}
	providers, err := parseReleaseReceipt(tag)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"core": "core@0.1.0", "engine": ""}, providers)

	tag.Annotated = false
	providers, err = parseReleaseReceipt(tag)
	require.NoError(t, err)
	assert.Nil(t, providers, "a lightweight tag's subject is a source commit message")
	tag.Annotated = true
	tag.Subject = "release cli@1.0.0"
	providers, err = parseReleaseReceipt(tag)
	require.NoError(t, err)
	assert.Nil(t, providers, "old release tags keep ancestry-only behavior")
}

func TestReleaseReceiptRejectsDamagedEvidence(t *testing.T) {
	for _, subject := range []string{
		"release cli@1.0.0 dispat-seen-v2:e30",
		"release other@1.0.0 dispat-seen-v1:e30",
		"release cli@1.0.0 dispat-seen-v1:!",
		"release cli@1.0.0 dispat-seen-v1:" + base64.RawURLEncoding.EncodeToString([]byte(`{"core":"one","core":"two"}`)),
	} {
		_, err := parseReleaseReceipt(gitx.Tag{Name: "cli@1.0.0", Annotated: true, Subject: subject})
		assert.Error(t, err, subject)
	}
}

func TestGeneratedReceiptLimitPreservesLegacyReadLimit(t *testing.T) {
	providers := make(map[string]string)
	for i := 0; i < 100; i++ {
		providers[fmt.Sprintf("provider%03d%s", i, strings.Repeat("x", 150))] = ""
	}
	_, err := EncodeProviderReceipt(providers)
	require.ErrorContains(t, err, "publish limit")
	_, err = RenderReleaseTagMessage("app@0.1.0", providers)
	require.ErrorContains(t, err, "publish limit")
	bytes, err := json.Marshal(providers)
	require.NoError(t, err)
	decoded, err := DecodeProviderReceipt(base64.RawURLEncoding.EncodeToString(bytes))
	require.NoError(t, err, "older canonical tags within the decoder limit remain readable")
	assert.Equal(t, providers, decoded)
}
