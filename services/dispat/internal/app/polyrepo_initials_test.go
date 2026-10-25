package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func TestPolyrepoInitialVersionsRespectDeclarationOwnership(t *testing.T) {
	control := &config.File{
		Initials:        map[string]string{"CORE": "0.0.0", "APP": "9.0.0"},
		InitialVersions: map[string]ccme.Version{"CORE": {}, "APP": {Major: 9}},
	}
	imported := &config.File{
		Initials:        map[string]string{"aPP": "2.0.0", "core": "8.0.0"},
		InitialVersions: map[string]ccme.Version{"aPP": {Major: 2}, "core": {Major: 8}},
	}
	workspace := &config.Workspace{Repositories: []config.Repository{
		{Name: config.ControlRepository, Control: true, Config: control},
		{Name: "central", Config: control},
		{Name: "imported", Imported: true, Config: imported},
	}}
	var logs bytes.Buffer
	a := NewWorkspace(t.TempDir(), control, workspace, zerolog.New(&logs))
	pkgs := []*model.Package{
		{Name: "core", Repository: "central"},
		{Name: "app", Repository: "imported"},
		{Name: "new", Repository: "imported"},
	}
	assert.Equal(t, map[string]ccme.Version{"core": {}, "app": {Major: 2}}, a.initialVersions(pkgs))
	assert.Contains(t, logs.String(), `"package":"APP","repository":"control"`)
	assert.Contains(t, logs.String(), `"package":"core","repository":"imported"`)
	var scanned []scannedPackage
	for _, p := range pkgs {
		scanned = append(scanned, scannedPackage{pkg: p, mans: []scanner.Manifest{versioned("package.json", "3.0.0", true)}})
	}
	suggestions := a.manifestBaselines(scanned, filter.Result{})
	require.Len(t, suggestions, 1)
	assert.Equal(t, "new", suggestions[0].pkg.Name)
	// A subsequent operation sees config edits without a stale cached index.
	imported.Initials["NEW"] = "0.0.0"
	assert.Empty(t, a.manifestBaselines(scanned, filter.Result{}))
}

func BenchmarkPolyrepoInitialVersionIndexes(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			cfg := &config.File{Initials: make(map[string]string, count), InitialVersions: make(map[string]ccme.Version, count)}
			pkgs := make([]*model.Package, count)
			scanned := make([]scannedPackage, count)
			for i := range count {
				name := fmt.Sprintf("pkg%d", i)
				cfg.Initials[strings.ToUpper(name)] = "1.0.0"
				cfg.InitialVersions[strings.ToUpper(name)] = ccme.Version{Major: 1}
				pkgs[i] = &model.Package{Name: name, Repository: "source"}
				scanned[i] = scannedPackage{pkg: pkgs[i]}
			}
			workspace := &config.Workspace{Repositories: []config.Repository{
				{Name: config.ControlRepository, Control: true, Config: cfg},
				{Name: "source", Config: cfg},
			}}
			a := NewWorkspace("", cfg, workspace, zerolog.Nop())
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if len(a.initialVersions(pkgs)) != count || len(a.manifestBaselines(scanned, filter.Result{})) != 0 {
					b.Fatal("incorrect indexed baseline selection")
				}
			}
		})
	}
}

// TestUnmatchedInitialsWarnInKeyOrder: an initials entry naming no package is
// reported once per entry, in key order rather than in the order the map
// happens to be walked, for a single history and for a composed workspace.
func TestUnmatchedInitialsWarnInKeyOrder(t *testing.T) {
	keys := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"}
	cfg := &config.File{InitialVersions: map[string]ccme.Version{"core": {Major: 1}}}
	for _, key := range keys {
		cfg.InitialVersions[key] = ccme.Version{Major: 1}
	}
	pkgs := []*model.Package{{Name: "core", Repository: "central"}}
	for name, workspace := range map[string]*config.Workspace{
		"single history": nil,
		"composed workspace": {Repositories: []config.Repository{
			{Name: config.ControlRepository, Control: true, Config: cfg},
			{Name: "central", Config: cfg},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			a := NewWorkspace(t.TempDir(), cfg, workspace, zerolog.New(&logs))

			assert.Equal(t, map[string]ccme.Version{"core": {Major: 1}}, a.initialVersions(pkgs))

			var warned []string
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var e struct{ Package string }
				require.NoError(t, json.Unmarshal([]byte(line), &e))
				warned = append(warned, e.Package)
			}
			assert.Equal(t, keys, warned)
		})
	}
}
