// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the `execution` key, before anything executes anywhere else.
//
// Two claims live here. The first is the one every later gate is measured
// against: a repository that says nothing about execution, or says it has no
// workers, releases exactly what it released before, reaches no mailbox and
// needs no secret. The second is that a configuration a distributed run could
// not be started under is refused while the only thing that has happened is
// that a file was read: one code, one key path, no tag.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionRefusalCode is the diagnostic every execution-configuration
// refusal carries, which is what a CI job switches on instead of matching a
// sentence.
const executionRefusalCode = "E225"

// executionLocalRepo is the one-package fixture the unchanged-release claim
// is made on: a package, a bare remote to look for coordination branches on,
// and whatever execution settings the row states.
func executionLocalRepo(t *testing.T, writeConfig func(*harness.Repo)) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	writeConfig(r)
	r.Commit("feat(core): bootstrap")
	r.AddBareRemote()
	return r
}

// executionRunShape is what "the release is unchanged" is compared on: every
// log line the run wrote, as the level, package, stage, code and message that
// line carried. The timestamps and durations are left out because they are
// the two fields that differ between two identical runs; everything a reader
// of the run learns is in the rest.
func executionRunShape(res harness.RunResult) []string {
	shape := make([]string, 0, len(res.Events))
	for _, e := range res.Events {
		shape = append(shape, strings.Join([]string{
			e.Str("level"), e.Package(), e.Str("stage"), e.Code(), e.Str("message"),
		}, "|"))
	}
	return shape
}

// executionEvents are the log lines of one invocation from both streams. A
// configuration refused before the configured logger exists is written by the
// bootstrap logger on stderr, and the diagnostic code is a structured field
// there exactly as it is in the run's own stream.
func executionEvents(res harness.RunResult) []harness.Event {
	return append(harness.ParseEvents(res.Stdout), harness.ParseEvents(res.Stderr)...)
}

// executionRemoteBranches lists the branches the run left on the remote, which
// for a local release is none at all and for a distributed one would be the
// coordination branches a mailbox carries.
func executionRemoteBranches(r *harness.Repo) []string {
	out := strings.TrimSpace(r.Git("ls-remote", "--heads", "origin"))
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// TestExecutionAbsentKeepsLocalReleaseUnchanged: the same repository released
// with no execution key, with an empty worker list, and with an orchestrator
// role and a capacity of its own produces the same tags and the same run, and
// none of the three reaches a mailbox or needs a signing secret. This is the
// fence every later gate is measured against: worker nodes are an addition,
// not a second path every existing repository travels.
func TestExecutionAbsentKeepsLocalReleaseUnchanged(t *testing.T) {
	writeTyped := func(execution *models.ExecutionConfig) func(*harness.Repo) {
		return func(r *harness.Repo) {
			cfg := libsConfig(echoBuild, 1)
			cfg.Execution = execution
			r.WriteConfigModel(cfg)
		}
	}
	var wantTags []string
	var wantShape []string
	for _, row := range []struct {
		name  string
		write func(*harness.Repo)
	}{
		{"no execution key", writeTyped(nil)},
		{"an empty worker list", func(r *harness.Repo) {
			// The one shape the typed model cannot express: omitempty drops a
			// present empty list on the way out.
			cfg := libsConfig(echoBuild, 1)
			raw := map[string]any{}
			data, err := json.Marshal(cfg)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &raw))
			raw["execution"] = map[string]any{"workers": []any{}}
			r.WriteConfigRaw(raw)
		}},
		{"an orchestrator with a capacity", writeTyped(&models.ExecutionConfig{
			Role: models.ExecutionRoleOrchestrator, Concurrency: models.Int(2),
		})},
	} {
		t.Run(row.name, func(t *testing.T) {
			r := executionLocalRepo(t, row.write)
			res := r.ReleaseOK()
			tags := r.TagList()
			shape := executionRunShape(res)
			require.Equal(t, []string{"core@0.1.0"}, tags)
			assert.Empty(t, executionRemoteBranches(r),
				"a local release leaves no coordination branch behind")
			assert.False(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
				"a local release is never refused for its execution settings")
			if wantTags == nil {
				wantTags, wantShape = tags, shape
				return
			}
			assert.Equal(t, wantTags, tags, "the tags a release writes do not depend on the execution key")
			assert.Equal(t, wantShape, shape, "the run a reader sees does not depend on the execution key")
		})
	}
}

// TestExecutionAcceptsEveryMailboxForm: the address forms a mailbox may be
// written in are accepted through the binary, so a deployment that reaches
// its nodes over ssh, over https, over a shared filesystem or through the
// scp-like form git has always taken is not refused for the spelling. Read
// through `dispat status`, which is where a configuration is checked and
// nothing is executed.
func TestExecutionAcceptsEveryMailboxForm(t *testing.T) {
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	cfg := libsConfig(echoBuild, 1)
	cfg.Execution = &models.ExecutionConfig{
		Role:      models.ExecutionRoleOrchestrator,
		Name:      "control",
		Endpoint:  "ssh://git.example.test/srv/control.git",
		SecretEnv: "DISPAT_IT_EXECUTION_SECRET",
		Workers: []models.ExecutionWorkerConfig{
			{Name: "https", Endpoint: "https://git.example.test/a.git"},
			{Name: "ssh", Endpoint: "ssh://git.example.test/a.git"},
			{Name: "ssh-account", Endpoint: "ssh://git@git.example.test/a.git"},
			{Name: "file", Endpoint: "file:///srv/a.git"},
			{Name: "absolute", Endpoint: "/srv/a.git"},
			{Name: "scp", Endpoint: "git@git.example.test:a.git"},
			{Name: "scp-without-user", Endpoint: "git.example.test:a.git"},
			// No endpoint: the repository being released, whose remote this
			// repository does not even have. Status resolves no remote.
			{Name: "the-repository"},
		},
		Timeouts: &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 600, Cancel: 15},
		Transfer: &models.ExecutionTransferConfig{MaxFiles: 10, MaxBytes: 4096, MaxManifestBytes: 1024, Timeout: 60},
	}
	r.WriteConfigModel(cfg)
	r.Commit("feat(core): bootstrap")
	res := r.StatusOK("--log-format", "json")
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode))
	// Nothing consumes the settings yet, so the plan is the plan it always
	// was and no mailbox was reached.
	assert.Equal(t, "● changed", harness.GraphLine(res.Events, "core").Str("message"))
	assert.Empty(t, r.TagList(), "status releases nothing")
}

// TestExecutionRefusesAnUnreadableNumber: the ceilings are numbers, and a
// value that is not one is refused by the decoder naming the key rather than
// being read as a zero that would silently become the default.
func TestExecutionRefusesAnUnreadableNumber(t *testing.T) {
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	raw := map[string]any{}
	data, err := json.Marshal(libsConfig(echoBuild, 1))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &raw))
	raw["execution"] = map[string]any{"transfer": map[string]any{"maxBytes": "as much as it takes"}}
	r.WriteConfigRaw(raw)
	r.Commit("feat(core): bootstrap")
	res := r.Status("--log-format", "json")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, diagnosticText(res), "execution.transfer.maxBytes")
	assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
}

// executionRefusal is one row of the refusal table: the execution object the
// configuration states, and the key path the reader is owed for it.
type executionRefusal struct {
	execution *models.ExecutionConfig
	want      string
}

// executionLink builds the one-link configuration the endpoint rules are
// varied through.
func executionLink(endpoint string) *models.ExecutionConfig {
	return &models.ExecutionConfig{
		SecretEnv: "DISPAT_IT_EXECUTION_SECRET",
		Workers:   []models.ExecutionWorkerConfig{{Name: "build-a", Endpoint: endpoint}},
	}
}

// executionStated returns a row stating one execution object.
func executionStated(execution *models.ExecutionConfig, want string) executionRefusal {
	return executionRefusal{execution: execution, want: want}
}

// TestExecutionConfigRefusals: every rule the `execution` key is held to,
// through the binary. A refused configuration exits non-zero, names the key
// path that is wrong, carries the execution diagnostic, and leaves the
// repository exactly as it found it, because nothing about a release has
// happened yet.
func TestExecutionConfigRefusals(t *testing.T) {
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.Commit("feat(core): bootstrap")
	for name, row := range map[string]executionRefusal{
		"an unknown role": executionStated(
			&models.ExecutionConfig{Role: "coordinator"}, "execution.role"),
		"a role spelled with the wrong case": executionStated(
			&models.ExecutionConfig{Role: "Orchestrator"}, "execution.role"),
		"a capacity of zero": executionStated(
			&models.ExecutionConfig{Concurrency: models.Int(0)}, "execution.concurrency"),
		"a negative capacity": executionStated(
			&models.ExecutionConfig{Concurrency: models.Int(-1)}, "execution.concurrency"),
		"a negative wait": executionStated(
			&models.ExecutionConfig{Timeouts: &models.ExecutionTimeoutsConfig{Cancel: -1}},
			"execution.timeouts.cancel"),
		"a negative ceiling": executionStated(
			&models.ExecutionConfig{Transfer: &models.ExecutionTransferConfig{MaxManifestBytes: -1}},
			"execution.transfer.maxManifestBytes"),
		"a node name that is not one": executionStated(
			&models.ExecutionConfig{Name: "build a"}, "execution.name"),
		"a node name git could not write into a branch name": executionStated(
			&models.ExecutionConfig{Name: "build..a"}, "execution.name"),
		"a mailbox of this node that is not a remote": executionStated(
			&models.ExecutionConfig{Endpoint: "../mailbox.git"}, "execution.endpoint"),
		"a secret variable that is not a variable name": executionStated(
			&models.ExecutionConfig{SecretEnv: "2secret"}, "execution.secretEnv"),
		"a worker that delegates": executionStated(&models.ExecutionConfig{
			Role: models.ExecutionRoleWorker, SecretEnv: "DISPAT_IT_EXECUTION_SECRET",
			Workers: []models.ExecutionWorkerConfig{{Name: "build-a", Endpoint: "/srv/a.git"}},
		}, "execution.workers"),
		"a link with no name": executionStated(&models.ExecutionConfig{
			SecretEnv: "DISPAT_IT_EXECUTION_SECRET",
			Workers:   []models.ExecutionWorkerConfig{{Endpoint: "/srv/a.git"}},
		}, "execution.workers[0]: name is required"),
		"a link name that is not one": executionStated(&models.ExecutionConfig{
			SecretEnv: "DISPAT_IT_EXECUTION_SECRET",
			Workers:   []models.ExecutionWorkerConfig{{Name: "build/a", Endpoint: "/srv/a.git"}},
		}, "execution.workers[0]: name"),
		"two links naming one node": executionStated(&models.ExecutionConfig{
			SecretEnv: "DISPAT_IT_EXECUTION_SECRET",
			Workers: []models.ExecutionWorkerConfig{
				{Name: "build-a", Endpoint: "/srv/a.git"},
				{Name: "BUILD-A", Endpoint: "/srv/b.git"},
			},
		}, "execution.workers[1]: name"),
		"links with no signing secret": executionStated(&models.ExecutionConfig{
			Workers: []models.ExecutionWorkerConfig{{Name: "build-a", Endpoint: "/srv/a.git"}},
		}, "execution.secretEnv is required with execution.workers"),
		"a mailbox with credentials": executionStated(
			executionLink("https://user:secret@git.example.test/a.git"), "execution.workers[0]: endpoint"),
		"a mailbox with a token in the https user half": executionStated(
			executionLink("https://token@git.example.test/a.git"), "execution.workers[0]: endpoint"),
		"a mailbox with a password beside the ssh account": executionStated(
			executionLink("ssh://git:secret@git.example.test/a.git"), "execution.workers[0]: endpoint"),
		"a mailbox with a password in the scp form": executionStated(
			executionLink("git:secret@git.example.test:a.git"), "execution.workers[0]: endpoint"),
		"a mailbox with a query": executionStated(
			executionLink("https://git.example.test/a.git?token=x"), "execution.workers[0]: endpoint"),
		"a mailbox with a fragment": executionStated(
			executionLink("https://git.example.test/a.git#main"), "execution.workers[0]: endpoint"),
		"a mailbox that would be read as an option": executionStated(
			executionLink("--upload-pack=touch"), "execution.workers[0]: endpoint"),
		"a mailbox naming a transport helper": executionStated(
			executionLink("ext::sh -c touch"), "execution.workers[0]: endpoint"),
		"a mailbox over http": executionStated(
			executionLink("http://git.example.test/a.git"), "execution.workers[0]: endpoint"),
		"a mailbox over the git protocol": executionStated(
			executionLink("git://git.example.test/a.git"), "execution.workers[0]: endpoint"),
		"a mailbox over an unknown scheme": executionStated(
			executionLink("ftp://git.example.test/a.git"), "execution.workers[0]: endpoint"),
		"a mailbox that is not a URL": executionStated(
			executionLink("https://exa mple.test/a.git"), "execution.workers[0]: endpoint"),
		"a mailbox with no host": executionStated(
			executionLink("https:///a.git"), "execution.workers[0]: endpoint"),
		"a mailbox written as a relative path": executionStated(
			executionLink("mailboxes/a.git"), "execution.workers[0]: endpoint"),
		"a mailbox carrying both a password and a token": executionStated(
			executionLink("git:secret@git.example.test:a.git?token=x"), "execution.workers[0]: endpoint"),
	} {
		t.Run(name, func(t *testing.T) {
			cfg := libsConfig(echoBuild, 1)
			cfg.Execution = row.execution
			r.WriteConfigModel(cfg)
			res := r.Status("--log-format", "json")
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, diagnosticText(res), row.want)
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
				"no %s diagnostic\nstdout:\n%s\nstderr:\n%s", executionRefusalCode, res.Stdout, res.Stderr)
			assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
		})
	}
}

// TestExecutionRefusalNeverPrintsACredential: a mailbox address is refused
// because of what it carries, so the refusal is the one place a credential in
// it would otherwise be written into a CI log. The address is still named,
// because a reader with several links needs to know which one is wrong.
func TestExecutionRefusalNeverPrintsACredential(t *testing.T) {
	for name, endpoint := range map[string]string{
		"a password in the scp form": "git:hunter2@git.example.test:a.git?token=swordfish",
		"a password in a URL":        "https://user:hunter2@git.example.test/a.git",
		"a token in a query":         "https://git.example.test/a.git?token=swordfish",
		"a token in a fragment":      "https://git.example.test/a.git#swordfish",
		"a password behind a dash":   "-u:hunter2@git.example.test:a.git",
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			r.SeedPackage("packages", "core")
			cfg := libsConfig(echoBuild, 1)
			cfg.Execution = executionLink(endpoint)
			r.WriteConfigModel(cfg)
			r.Commit("feat(core): bootstrap")
			res := r.Status("--log-format", "json")
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			text := diagnosticText(res)
			assert.NotContains(t, text, "hunter2")
			assert.NotContains(t, text, "swordfish")
			assert.Contains(t, text, "execution.workers[0]: endpoint")
		})
	}
}

// TestExecutionIsRefusedOutsideTheRootFile: the key is a node-startup setting,
// so a space, a package entry and a package folder's own file each refuse it
// as an unknown key. A checkout that travels to another machine must not be
// able to tell that machine what role it plays.
func TestExecutionIsRefusedOutsideTheRootFile(t *testing.T) {
	stated := map[string]any{"role": "worker"}
	for name, write := range map[string]func(*harness.Repo, map[string]any){
		"in a space": func(r *harness.Repo, raw map[string]any) {
			spaces := raw["spaces"].(map[string]any)
			libs := spaces["libs"].(map[string]any)
			libs["execution"] = stated
			r.WriteConfigRaw(raw)
		},
		"in a package entry": func(r *harness.Repo, raw map[string]any) {
			raw["packages"] = map[string]any{"core": map[string]any{"execution": stated}}
			r.WriteConfigRaw(raw)
		},
		"in a package folder's own file": func(r *harness.Repo, raw map[string]any) {
			r.WriteConfigRaw(raw)
			r.WriteFile(filepath.Join("packages", "core", "dispat.json"),
				executionRawJSON(r, map[string]any{"execution": stated}))
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			r.SeedPackage("packages", "core")
			raw := map[string]any{}
			data, err := json.Marshal(libsConfig(echoBuild, 1))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &raw))
			write(r, raw)
			r.Commit("feat(core): bootstrap")
			res := r.Status("--log-format", "json")
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			text := diagnosticText(res)
			assert.Contains(t, text, "execution")
			assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
		})
	}
}

// executionRawJSON renders a raw configuration for the folder files the
// harness has no typed writer for.
func executionRawJSON(r *harness.Repo, raw map[string]any) string {
	r.T.Helper()
	data, err := json.MarshalIndent(raw, "", "  ")
	require.NoError(r.T, err)
	return string(data) + "\n"
}

// TestExecutionInAnImportedConfigIsValidatedNotConsulted: an imported
// repository's own `execution` object is legal to state, because any peer may
// be another run's entry, and the entry configuration is the only one a run
// reads. It is still validated when that file is loaded: a file that cannot
// say what it means is a file nobody should be running from either.
func TestExecutionInAnImportedConfigIsValidatedNotConsulted(t *testing.T) {
	newFleet := func(t *testing.T, execution *models.ExecutionConfig) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		sourceCfg := polyrepoModelFile()
		sourceCfg.Polyrepo = false
		sourceCfg.Spaces = polyrepoModelSpaces(map[string]string{"libs": "packages"})
		sourceCfg.Execution = execution
		source.WriteConfigModel(sourceCfg)
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := polyrepoModelFile()
		cfg.Configs = []string{"sources/lib/dispat.json"}
		control.WriteConfigModel(cfg)
		control.Commit("chore: import the source configuration")
		return control
	}

	t.Run("a worker role an imported peer states is ignored", func(t *testing.T) {
		// The peer calls itself a worker; the run is started here, so the run
		// is an orchestrator's and the peer's own settings are never read.
		control := newFleet(t, &models.ExecutionConfig{
			Role: models.ExecutionRoleWorker, Name: "build-a", Endpoint: "/srv/mailboxes/a.git",
		})
		res := control.Status("--log-format", "json")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.False(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode))
	})

	t.Run("a malformed object still fails its own file's load", func(t *testing.T) {
		control := newFleet(t, &models.ExecutionConfig{Role: "coordinator"})
		res := control.Status("--log-format", "json")
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, diagnosticText(res), "execution.role")
		assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
			"no %s diagnostic\nstdout:\n%s\nstderr:\n%s", executionRefusalCode, res.Stdout, res.Stderr)
	})
}
