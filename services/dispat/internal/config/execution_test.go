package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

// The execution key is refused at load or it is not refused at all: nothing
// reads it before the locks, so a mistake in it has to be caught while the
// only thing that has happened is that a file was read. What is tested here
// is each rule on its own, that a configuration saying nothing still loads
// with the key absent rather than filled in, and that the key belongs to the
// root file alone.

// executionConfig is the minimal configuration with an execution object,
// which every row below varies one field of.
func executionConfig(x *ExecutionConfig) File {
	cfg := minimalConfig()
	cfg.Execution = x
	return cfg
}

// orchestratorWithWorkers is the shape a distributed run is configured with:
// two links and the variable naming the secret their messages are signed
// with.
func orchestratorWithWorkers() *ExecutionConfig {
	return &ExecutionConfig{
		Role:        models.ExecutionRoleOrchestrator,
		Concurrency: models.Int(2),
		SecretEnv:   "DISPAT_EXECUTION_SECRET",
		Workers: []ExecutionWorkerConfig{
			{Name: "Build-A", Endpoint: "https://git.example.test/mailbox-a.git"},
			{Name: "build-b", Endpoint: "git@git.example.test:mailbox-b.git"},
		},
	}
}

func TestLoadExecutionAbsentStaysAbsent(t *testing.T) {
	// nil is what keeps a repository that has never heard of worker nodes on
	// exactly the path it was on, so nothing may fill the object in.
	loaded, err := loadModel(t, minimalConfig(), "pkgs/core")
	require.NoError(t, err)
	assert.Nil(t, loaded.Execution)
	assert.Equal(t, models.ExecutionRoleOrchestrator, loaded.Execution.ResolveRole())
	assert.False(t, loaded.Execution.IsDistributed())
}

func TestLoadExecutionAcceptedShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		execution *ExecutionConfig
		assert    func(*testing.T, *File)
	}{
		"an orchestrator with two workers": {orchestratorWithWorkers(), func(t *testing.T, loaded *File) {
			require.Len(t, loaded.Execution.Workers, 2)
			// The node names keep the case the file wrote them in, which is
			// the whole reason the links are a list rather than a map.
			assert.Equal(t, "Build-A", loaded.Execution.Workers[0].Name)
			assert.True(t, loaded.Execution.IsDistributed())
			assert.Equal(t, 2, loaded.Execution.ResolveConcurrency())
		}},
		"a worker node": {&ExecutionConfig{
			Role:      models.ExecutionRoleWorker,
			Name:      "build-a",
			Endpoint:  "/srv/mailboxes/build-a.git",
			SecretEnv: "DISPAT_EXECUTION_SECRET",
		}, func(t *testing.T, loaded *File) {
			assert.True(t, loaded.Execution.IsWorker())
			assert.False(t, loaded.Execution.IsDistributed())
			// A worker states no capacity here, so it takes the default.
			assert.Equal(t, models.DefaultExecutionConcurrency, loaded.Execution.ResolveConcurrency())
		}},
		"stated bounds": {&ExecutionConfig{
			Timeouts: &ExecutionTimeoutsConfig{Preflight: 10, Task: 20, Cancel: 30},
			Transfer: &ExecutionTransferConfig{MaxFiles: 1, MaxBytes: 2, MaxManifestBytes: 3, Timeout: 4},
		}, func(t *testing.T, loaded *File) {
			assert.Equal(t, ExecutionTimeoutsConfig{Preflight: 10, Task: 20, Cancel: 30},
				loaded.Execution.ResolveTimeouts())
			assert.Equal(t, ExecutionTransferConfig{MaxFiles: 1, MaxBytes: 2, MaxManifestBytes: 3, Timeout: 4},
				loaded.Execution.ResolveTransfer())
		}},
		"every endpoint form": {&ExecutionConfig{
			SecretEnv: "S",
			Workers: []ExecutionWorkerConfig{
				{Name: "https", Endpoint: "https://git.example.test/a.git"},
				{Name: "ssh", Endpoint: "ssh://git.example.test/a.git"},
				{Name: "ssh-account", Endpoint: "ssh://git@git.example.test/a.git"},
				{Name: "file", Endpoint: "file:///srv/a.git"},
				{Name: "absolute", Endpoint: "/srv/a.git"},
				{Name: "scp", Endpoint: "git@git.example.test:a.git"},
				{Name: "scp-without-user", Endpoint: "git.example.test:a.git"},
			},
		}, func(t *testing.T, loaded *File) {
			assert.Len(t, loaded.Execution.Workers, 7)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			loaded, err := loadModel(t, executionConfig(tc.execution), "pkgs/core")
			require.NoError(t, err)
			tc.assert(t, loaded)
		})
	}
}

func TestLoadExecutionEmptyWorkerListIsLocalExecution(t *testing.T) {
	// A present but empty list is the one shape the model cannot express:
	// omitempty drops it on the way out. It means what an absent list means,
	// and it needs no secret because nothing is dispatched.
	root := writeRawRepo(t, map[string]any{
		"scripts":   map[string]any{"build": []string{"echo b"}},
		"spaces":    map[string]any{"libs": map[string]any{"path": []string{"pkgs"}}},
		"execution": map[string]any{"workers": []any{}},
	}, "pkgs/core")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	assert.False(t, loaded.Execution.IsDistributed())
}

func TestLoadExecutionRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		execution *ExecutionConfig
		want      string
	}{
		"an unknown role": {
			&ExecutionConfig{Role: "coordinator"}, `execution.role "coordinator" is invalid`},
		"a role spelled with the wrong case": {
			&ExecutionConfig{Role: "Worker"}, `execution.role "Worker" is invalid`},
		"a capacity of zero": {
			&ExecutionConfig{Concurrency: models.Int(0)}, "execution.concurrency must be >= 1, got 0"},
		"a negative capacity": {
			&ExecutionConfig{Concurrency: models.Int(-2)}, "execution.concurrency must be >= 1, got -2"},
		"a negative wait": {
			&ExecutionConfig{Timeouts: &ExecutionTimeoutsConfig{Task: -1}},
			"execution.timeouts.task must be >= 0, got -1"},
		"a negative ceiling": {
			&ExecutionConfig{Transfer: &ExecutionTransferConfig{MaxBytes: -3}},
			"execution.transfer.maxBytes must be >= 0, got -3"},
		"a node name that is not one": {
			&ExecutionConfig{Name: "build a"}, `execution.name "build a" is not a node name`},
		"a node name git could not write into a branch name": {
			&ExecutionConfig{Name: "build..a"}, `execution.name "build..a" is not a node name`},
		"an endpoint of this node that is not a remote": {
			&ExecutionConfig{Endpoint: "../mailbox.git"}, "execution.endpoint: "},
		"a secret variable that is not a variable name": {
			&ExecutionConfig{SecretEnv: "2secret"}, `execution.secretEnv "2secret" is not an environment variable name`},
		"a worker that delegates": {
			&ExecutionConfig{Role: models.ExecutionRoleWorker, SecretEnv: "S",
				Workers: []ExecutionWorkerConfig{{Name: "a", Endpoint: "/srv/a.git"}}},
			"execution.workers is not a worker's to state"},
		"a link with no name": {
			&ExecutionConfig{SecretEnv: "S", Workers: []ExecutionWorkerConfig{{Endpoint: "/srv/a.git"}}},
			"execution.workers[0]: name is required"},
		"a link name that is not one": {
			&ExecutionConfig{SecretEnv: "S", Workers: []ExecutionWorkerConfig{{Name: "build/a", Endpoint: "/srv/a.git"}}},
			`execution.workers[0]: name "build/a" is not a node name`},
		"a link name git could not write into a branch name": {
			&ExecutionConfig{SecretEnv: "S", Workers: []ExecutionWorkerConfig{{Name: "a..b", Endpoint: "/srv/a.git"}}},
			`execution.workers[0]: name "a..b" is not a node name`},
		"two links naming one node": {
			&ExecutionConfig{SecretEnv: "S", Workers: []ExecutionWorkerConfig{
				{Name: "build-a", Endpoint: "/srv/a.git"},
				{Name: "BUILD-A", Endpoint: "/srv/b.git"},
			}},
			`execution.workers[1]: name "BUILD-A" is already used by execution.workers[0]`},
		"a link with no mailbox": {
			&ExecutionConfig{SecretEnv: "S", Workers: []ExecutionWorkerConfig{{Name: "a"}}},
			"execution.workers[0]: endpoint is required"},
		"a mailbox with credentials": {
			executionWithEndpoint("https://user:secret@git.example.test/a.git"),
			"carries user information"},
		"a mailbox with a token in the https user half": {
			executionWithEndpoint("https://token@git.example.test/a.git"), "carries user information"},
		"a mailbox with a password beside the ssh account": {
			executionWithEndpoint("ssh://git:secret@git.example.test/a.git"), "carries user information"},
		"a mailbox with a password in the scp form": {
			executionWithEndpoint("git:secret@git.example.test:a.git"), "carries a password"},
		"a mailbox with a query": {
			executionWithEndpoint("https://git.example.test/a.git?token=x"), "carries a query"},
		"a mailbox with a fragment": {
			executionWithEndpoint("https://git.example.test/a.git#main"), "carries a fragment"},
		"a mailbox that would be read as an option": {
			executionWithEndpoint("--upload-pack=touch"), "reads as an option"},
		"a mailbox naming a transport helper": {
			executionWithEndpoint("ext::sh -c touch"), "names a helper program to run"},
		"a mailbox over http": {
			executionWithEndpoint("http://git.example.test/a.git"), "which authenticates nobody"},
		"a mailbox over the git protocol": {
			executionWithEndpoint("git://git.example.test/a.git"), "which authenticates nobody"},
		"a mailbox over an unknown scheme": {
			executionWithEndpoint("ftp://git.example.test/a.git"), "uses the ftp scheme"},
		"a mailbox that is not a URL": {
			executionWithEndpoint("https://exa mple.test/a.git"), "is not a URL git can fetch from"},
		"a mailbox with no host": {
			executionWithEndpoint("https:///a.git"), "names no host"},
		"a mailbox written as a relative path": {
			executionWithEndpoint("mailboxes/a.git"), "is not a git remote"},
		"a mailbox carrying both a password and a token": {
			executionWithEndpoint("git:secret@git.example.test:a.git?token=x"), "carries a query"},
		"links with no signing secret": {
			&ExecutionConfig{Workers: []ExecutionWorkerConfig{{Name: "a", Endpoint: "/srv/a.git"}}},
			"execution.secretEnv is required with execution.workers"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadModel(t, executionConfig(tc.execution), "pkgs/core")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			// The code is what a CI job switches on, so every arm carries it
			// rather than only the ones that read like configuration.
			assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
		})
	}
}

func TestLoadExecutionRefusalNeverEchoesACredential(t *testing.T) {
	// A refused mailbox is refused because of what it carries, so the refusal
	// is the one place its credentials would otherwise be written down. The
	// address is still named, because a reader with several links needs to
	// know which one is wrong.
	for name, endpoint := range map[string]string{
		"a password in the scp form": "git:hunter2@git.example.test:a.git?token=swordfish",
		"a password in a URL":        "https://user:hunter2@git.example.test/a.git",
		"a token in a query":         "https://git.example.test/a.git?token=swordfish",
		"a token in a fragment":      "https://git.example.test/a.git#swordfish",
		"a password behind a dash":   "-u:hunter2@git.example.test:a.git",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadModel(t, executionConfig(executionWithEndpoint(endpoint)), "pkgs/core")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "hunter2")
			assert.NotContains(t, err.Error(), "swordfish")
			assert.Contains(t, err.Error(), "execution.workers[0]: endpoint")
		})
	}
}

// executionWithEndpoint is the one-link configuration the endpoint rules are
// varied through. The rules are the same for this node's own mailbox and for
// a link's, and a link is the shape that carries a name beside it.
func executionWithEndpoint(endpoint string) *ExecutionConfig {
	return &ExecutionConfig{
		SecretEnv: "DISPAT_EXECUTION_SECRET",
		Workers:   []ExecutionWorkerConfig{{Name: "build-a", Endpoint: endpoint}},
	}
}

func TestExecutionIsARootKeyOnly(t *testing.T) {
	// The key exists in fileFields alone, which is what makes a space, a
	// package entry, a space folder's file and a package folder's file refuse
	// it as an unknown key: a checkout travelling to another machine must not
	// be able to tell that machine what role it plays.
	stated := map[string]any{"execution": map[string]any{"role": "worker"}}
	t.Run("a space in the root file", func(t *testing.T) {
		root := writeRawRepo(t, map[string]any{
			"scripts": map[string]any{"build": []string{"echo b"}},
			"spaces": map[string]any{"libs": map[string]any{
				"path":      []string{"pkgs"},
				"execution": map[string]any{"role": "worker"},
			}},
		}, "pkgs/core")
		_, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "execution")
	})
	t.Run("a package entry in the root file", func(t *testing.T) {
		root := writeRawRepo(t, map[string]any{
			"scripts":  map[string]any{"build": []string{"echo b"}},
			"spaces":   map[string]any{"libs": map[string]any{"path": []string{"pkgs"}}},
			"packages": map[string]any{"core": stated},
		}, "pkgs/core")
		_, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "execution")
	})
	t.Run("a space folder's own file", func(t *testing.T) {
		var dst SpaceFile
		require.Error(t, decodeSpaceFile(stated, &dst))
	})
	t.Run("a package folder's own file", func(t *testing.T) {
		var dst PackageConfig
		require.Error(t, decodePackageConfig(stated, &dst))
	})
}
