package publicapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"gopkg.in/yaml.v3"
)

// Configuration generators must preserve all three sharing axes through
// either supported serialization format, including an omitted declaration.
func TestPublicAPIModelVersionGroupSerialization(t *testing.T) {
	for _, group := range []models.VersionGroupConfig{
		{},
		{Versioning: models.VersioningFixed},
		{Versioning: models.VersioningFixedMajorMinor, Counter: models.SharingIndependent, Channels: models.SharingFixed},
	} {
		t.Run(group.Versioning, func(t *testing.T) {
			encoded, err := json.Marshal(group)
			require.NoError(t, err)
			var decoded models.VersionGroupConfig
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, group, decoded)
			yamlBytes, err := yaml.Marshal(group)
			require.NoError(t, err)
			var document map[string]any
			require.NoError(t, yaml.Unmarshal(yamlBytes, &document))
			fromYAML, err := models.NormalizeVersionGroupVersioning(document["versioning"], "versionGroups.train.versioning")
			require.NoError(t, err)
			assert.Equal(t, group, fromYAML)
		})
	}

	for _, document := range []string{
		`{"unknown":"fixed"}`,
		`{"versioning":[]}`,
		`{"versioning":{"unknown":"fixed"}}`,
		`{"versioning":{"counter":"fixed","COUNTER":"independent"}}`,
		`{"versioning":{"channels":false}}`,
	} {
		t.Run(document, func(t *testing.T) {
			original := models.VersionGroupConfig{Versioning: models.VersioningFixed}
			group := original
			require.Error(t, json.Unmarshal([]byte(document), &group))
			assert.Equal(t, original, group, "a rejected update must not partially replace the previous policy")
		})
	}
}
