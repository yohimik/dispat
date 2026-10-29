package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFolderConfigsRefuseRemovedExecutionKeysBeforePruning(t *testing.T) {
	for _, key := range []string{"workers", "role"} {
		for _, spelling := range []string{"nested", "dotted", "mixed-case", "mixed-case-dotted"} {
			for valueName, value := range map[string]any{"empty-object": map[string]any{}, "null": nil, "empty-list": []any{}, "stated": "worker"} {
				for _, level := range []string{"package", "space"} {
					t.Run(level+"/"+key+"/"+spelling+"/"+valueName, func(t *testing.T) {
						var raw map[string]any
						switch spelling {
						case "dotted":
							raw = map[string]any{"execution." + key: value}
						case "mixed-case":
							raw = map[string]any{"ExEcUtIoN": map[string]any{strings.ToUpper(key): value}}
						case "mixed-case-dotted":
							raw = map[string]any{"ExEcUtIoN." + strings.ToUpper(key): value}
						default:
							raw = map[string]any{"execution": map[string]any{key: value}}
						}
						folder := t.TempDir()
						document, err := json.Marshal(raw)
						require.NoError(t, err)
						path := filepath.Join(folder, "dispat.json")
						require.NoError(t, os.WriteFile(path, document, 0o600))
						if level == "package" {
							_, found, err := loadPackageFile(folder)
							require.Equal(t, path, found)
							require.ErrorContains(t, err, "execution."+key+" was removed")
							require.Equal(t, DiagnosticExecution, DiagnosticCode(err))
							return
						}
						_, found, err := loadSpaceFile(folder)
						require.Equal(t, path, found)
						require.ErrorContains(t, err, "execution."+key+" was removed")
						require.Equal(t, DiagnosticExecution, DiagnosticCode(err))
					})
				}
			}
		}
	}
}

func TestInlineConfigLevelsRefuseRemovedExecutionKeysBeforePruning(t *testing.T) {
	for _, prefix := range [][]string{nil, {"packages", "core"}, {"spaces", "libs"}, {"spaces", "libs", "packages", "core"}} {
		for _, key := range []string{"workers", "role"} {
			for _, spelling := range []string{"nested", "dotted", "mixed-case", "mixed-case-dotted"} {
				for valueName, value := range map[string]any{"empty-object": map[string]any{}, "null": nil, "empty-list": []any{}, "stated": "worker"} {
					t.Run(strings.Join(prefix, "/")+"/"+key+"/"+spelling+"/"+valueName, func(t *testing.T) {
						path := append(prefix, "execution", key)
						if strings.HasPrefix(spelling, "mixed-case") {
							for index := range path {
								path[index] = strings.ToUpper(path[index])
							}
						}
						raw := removedExecutionTestDocument(path, value, strings.HasSuffix(spelling, "dotted"))
						folder := writeRawRepo(t, raw)
						_, err := Load(filepath.Join(folder, "dispat.json"), nil)
						require.ErrorContains(t, err, "execution."+key+" was removed")
						require.Equal(t, DiagnosticExecution, DiagnosticCode(err))
					})
				}
			}
		}
	}
}

func removedExecutionTestDocument(path []string, value any, isDotted bool) map[string]any {
	if isDotted {
		return map[string]any{strings.Join(path, "."): value}
	}
	for index := len(path) - 1; index >= 0; index-- {
		value = map[string]any{path[index]: value}
	}
	return value.(map[string]any)
}

func TestRemovedExecutionKeysDoNotInspectCustomPayloads(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"packages": map[string]any{"core": map[string]any{"path": "pkgs/core"}},
		"custom": map[string]any{"execution": map[string]any{"workers": "application data"},
			"spaces": map[string]any{"example": map[string]any{"execution": map[string]any{"role": "application data"}}}},
	}, "pkgs/core")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
}
