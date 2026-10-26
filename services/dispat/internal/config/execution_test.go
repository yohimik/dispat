package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"math"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

func executionTestPublicKey(t *testing.T) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(public)
}

func TestLoadExecutionBoundsAdmissionAtSignedProtocolMaximum(t *testing.T) {
	seconds := models.MaxExecutionAdmissionTimeoutSeconds
	_, err := loadModel(t, executionConfig(&ExecutionConfig{
		Timeouts: &ExecutionTimeoutsConfig{Admission: seconds},
	}), "pkgs/core")
	require.NoError(t, err)
	_, err = loadModel(t, executionConfig(&ExecutionConfig{
		Timeouts: &ExecutionTimeoutsConfig{Admission: seconds + 1},
	}), "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "execution.timeouts.admission must be <=")
}

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
		"a serving worker": {&ExecutionConfig{
			Name:      "build-a",
			Endpoint:  "/srv/mailboxes/build-a.git",
			SecretEnv: "DISPAT_EXECUTION_SECRET",
		}, func(t *testing.T, loaded *File) {
			assert.False(t, loaded.Execution.IsDistributed())
			// A worker states no capacity here, so it takes the default.
			assert.Equal(t, models.DefaultExecutionConcurrency, loaded.Execution.ResolveConcurrency())
		}},
		"stated bounds": {&ExecutionConfig{
			Timeouts: &ExecutionTimeoutsConfig{Preflight: 10, Admission: 15, Task: 20, Cancel: 30},
			Transfer: &ExecutionTransferConfig{MaxFiles: 1, MaxBytes: 2, MaxManifestBytes: 3, MaxPackBytes: 5, Timeout: 4},
		}, func(t *testing.T, loaded *File) {
			assert.Equal(t, ExecutionTimeoutsConfig{Preflight: 10, Admission: 15, Task: 20, Cancel: 30},
				loaded.Execution.ResolveTimeouts())
			assert.Equal(t, ExecutionTransferConfig{MaxFiles: 1, MaxBytes: 2, MaxManifestBytes: 3, MaxPackBytes: 5, Timeout: 4},
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
		// A link that states no endpoint reaches the repository being
		// released, whose remote a run resolves and checks when it starts:
		// the file has nothing to hold it to here.
		"a link that states no endpoint": {&ExecutionConfig{
			SecretEnv: "S",
			Workers:   []ExecutionWorkerConfig{{Name: "build-a"}, {Name: "build-b", Endpoint: "/srv/b.git"}},
		}, func(t *testing.T, loaded *File) {
			assert.Equal(t, []ExecutionWorkerConfig{{Name: "build-a"}, {Name: "build-b", Endpoint: "/srv/b.git"}},
				loaded.Execution.Workers)
			assert.True(t, loaded.Execution.IsDistributed())
		}},
		"only a preauthorized late worker": {&ExecutionConfig{
			SecretEnv: "S",
			Workers: []ExecutionWorkerConfig{{Name: "build-a", LateJoin: true,
				IdentityPublicKey: executionTestPublicKey(t)}},
		}, func(t *testing.T, loaded *File) {
			assert.True(t, loaded.Execution.IsDistributed())
			require.Len(t, loaded.Execution.Workers, 1)
			assert.True(t, loaded.Execution.Workers[0].LateJoin)
			assert.NotEmpty(t, loaded.Execution.Workers[0].IdentityPublicKey)
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

func TestRemovedExecutionKeysAreRefused(t *testing.T) {
	for _, key := range []string{"role", "workers"} {
		t.Run(key, func(t *testing.T) {
			root := writeRawRepo(t, map[string]any{
				"scripts":   map[string]any{"build": []string{"echo b"}},
				"spaces":    map[string]any{"libs": map[string]any{"path": []string{"pkgs"}}},
				"execution": map[string]any{key: "worker"},
			}, "pkgs/core")
			_, err := Load(filepath.Join(root, "dispat.json"), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), key)
		})
	}
}

func TestLoadExecutionRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		execution *ExecutionConfig
		want      string
	}{
		"a capacity of zero": {
			&ExecutionConfig{Concurrency: models.Int(0)}, "execution.concurrency must be >= 1, got 0"},
		"a negative capacity": {
			&ExecutionConfig{Concurrency: models.Int(-2)}, "execution.concurrency must be >= 1, got -2"},
		"a negative wait": {
			&ExecutionConfig{Timeouts: &ExecutionTimeoutsConfig{Task: -1}},
			"execution.timeouts.task must be >= 0, got -1"},
		"a negative admission deadline": {
			&ExecutionConfig{Timeouts: &ExecutionTimeoutsConfig{Admission: -1}},
			"execution.timeouts.admission must be >= 0, got -1"},
		"a negative ceiling": {
			&ExecutionConfig{Transfer: &ExecutionTransferConfig{MaxBytes: -3}},
			"execution.transfer.maxBytes must be >= 0, got -3"},
		"a negative Git pack ceiling": {
			&ExecutionConfig{Transfer: &ExecutionTransferConfig{MaxPackBytes: -3}},
			"execution.transfer.maxPackBytes must be >= 0, got -3"},
		"a Git pack ceiling that cannot be probed": {
			&ExecutionConfig{Transfer: &ExecutionTransferConfig{MaxPackBytes: math.MaxInt64}},
			"execution.transfer.maxPackBytes must be <= 9223372036854775806"},
		"a node name that is not one": {
			&ExecutionConfig{Name: "build a"}, `execution.name "build a" is not a node name`},
		"a node name git could not write into a branch name": {
			&ExecutionConfig{Name: "build..a"}, `execution.name "build..a" is not a node name`},
		"an endpoint of this node that is not a remote": {
			&ExecutionConfig{Endpoint: "../mailbox.git"}, "execution.endpoint: "},
		"a secret variable that is not a variable name": {
			&ExecutionConfig{SecretEnv: "2secret"}, `execution.secretEnv "2secret" is not an environment variable name`},
		"an identity seed variable that is not a variable name": {
			&ExecutionConfig{IdentityKeyEnv: "2seed"}, `execution.identityKeyEnv "2seed" is not an environment variable name`},
		"late admission with no pinned identity key": {
			&ExecutionConfig{SecretEnv: "S", Workers: []ExecutionWorkerConfig{{Name: "late", LateJoin: true}}},
			"execution.workers[0].identityPublicKey must be a canonical base64url Ed25519 public key"},
		"late admission with a malformed identity key": {
			&ExecutionConfig{SecretEnv: "S", Workers: []ExecutionWorkerConfig{{Name: "late", LateJoin: true,
				IdentityPublicKey: "not-a-key"}}},
			"execution.workers[0].identityPublicKey must be a canonical base64url Ed25519 public key"},
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
	// be able to state another machine's runtime settings.
	stated := map[string]any{"execution": map[string]any{"name": "build-a"}}
	t.Run("a space in the root file", func(t *testing.T) {
		root := writeRawRepo(t, map[string]any{
			"scripts": map[string]any{"build": []string{"echo b"}},
			"spaces": map[string]any{"libs": map[string]any{
				"path":      []string{"pkgs"},
				"execution": map[string]any{"name": "build-a"},
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

// loadWithWorkerFlags loads a configuration the way the command line does,
// with every `--worker` value the invocation passed.
func loadWithWorkerFlags(t *testing.T, cfg File, values ...string) (*File, error) {
	t.Helper()
	flags := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	flags.StringArray(WorkerFlag, nil, "")
	for _, value := range values {
		require.NoError(t, flags.Set(WorkerFlag, value))
	}
	root := writeModelRepo(t, cfg, "pkgs/core")
	return Load(filepath.Join(root, "dispat.json"), flags)
}

// TestWorkerFlagAddsLinksBesideTheFilesOwn: a pipeline that created a machine
// a minute ago names it on the command line, and the link lands after the
// file's own, held to every rule they are.
func TestWorkerFlagAddsLinksBesideTheFilesOwn(t *testing.T) {
	loaded, err := loadWithWorkerFlags(t, executionConfig(orchestratorWithWorkers()),
		"build-c=file:///srv/mailbox", "build-d=git@git.example.test:mailbox-d.git", "build-e")
	require.NoError(t, err)
	require.Len(t, loaded.Execution.Workers, 5)
	assert.Equal(t, ExecutionWorkerConfig{Name: "build-c", Endpoint: "file:///srv/mailbox"},
		loaded.Execution.Workers[2])
	assert.Equal(t, "build-d", loaded.Execution.Workers[3].Name)
	assert.Equal(t, ExecutionWorkerConfig{Name: "build-e"}, loaded.Execution.Workers[4],
		"a name alone is a link to the repository being released")

	secretOnly := executionConfig(&ExecutionConfig{SecretEnv: "DISPAT_EXECUTION_SECRET",
		Timeouts: &ExecutionTimeoutsConfig{Preflight: 120}})
	loaded, err = loadWithWorkerFlags(t, secretOnly, "w1=file:///srv/mailbox")
	require.NoError(t, err)
	assert.True(t, loaded.Execution.IsDistributed(),
		"a file that states only the secret and the waits delegates once the invocation names a node")

	loaded, err = loadWithWorkerFlags(t, minimalConfig())
	require.NoError(t, err)
	assert.Nil(t, loaded.Execution, "an invocation that names no node changes nothing")
}

// TestWorkerFlagRefusals: every rule a configured link is held to, applied to
// a link the invocation named, with the flag's own label so the refusal names
// what the operator typed.
func TestWorkerFlagRefusals(t *testing.T) {
	for name, row := range map[string]struct {
		cfg    File
		values []string
		want   string
		code   string
	}{
		"a value with an empty endpoint": {
			cfg: executionConfig(orchestratorWithWorkers()), values: []string{"build-c="},
			want: "name or name=endpoint", code: DiagnosticExecution},
		"a name that is not a node name": {
			cfg: executionConfig(orchestratorWithWorkers()), values: []string{"build..c=file:///m"},
			want: "--worker build..c: name", code: DiagnosticExecution},
		"a configured link spelled again": {
			cfg: executionConfig(orchestratorWithWorkers()), values: []string{"BUILD-A=file:///m"},
			want: "--worker BUILD-A: name \"BUILD-A\" is already used by execution.workers[0]",
			code: DiagnosticExecution},
		"two flags naming one node": {
			cfg: executionConfig(orchestratorWithWorkers()), values: []string{"w=file:///m", "W=file:///n"},
			want: "is already used by --worker w", code: DiagnosticExecution},
		"a credential in the endpoint": {
			cfg: executionConfig(orchestratorWithWorkers()), values: []string{"w=https://user:hunter2@example.test/m.git"},
			want: "--worker w: endpoint", code: DiagnosticExecution},
		"no signing secret named": {
			cfg: minimalConfig(), values: []string{"w=file:///m"},
			want: "execution.secretEnv is required", code: DiagnosticExecution},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadWithWorkerFlags(t, row.cfg, row.values...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), row.want)
			assert.NotContains(t, err.Error(), "hunter2", "a refused endpoint is never echoed")
			assert.Equal(t, row.code, DiagnosticCode(err))
		})
	}
}

// TestReadWorkerFlagAsksOnlyASetFlag: a flag set that does not declare the
// flag, and one that declares it and never set it, both name no node.
func TestReadWorkerFlagAsksOnlyASetFlag(t *testing.T) {
	assert.Nil(t, readWorkerFlag(nil))
	undeclared := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	assert.Nil(t, readWorkerFlag(undeclared))
	unset := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	unset.StringArray(WorkerFlag, nil, "")
	assert.Nil(t, readWorkerFlag(unset))
	scalar := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	scalar.String(WorkerFlag, "", "")
	require.NoError(t, scalar.Set(WorkerFlag, "w=file:///m"))
	assert.Nil(t, readWorkerFlag(scalar), "a flag of another shape is not a list of links")
}

// TestParseWorkerLinkShapes: a `--worker` value is a node name alone, which
// reaches the repository being released, or name=endpoint with both halves
// stated. Anything else is a usage mistake, refused without echoing what was
// typed, because a value that is not a node name is most likely an endpoint
// somebody wrote without its name.
func TestParseWorkerLinkShapes(t *testing.T) {
	for value, want := range map[string]ExecutionWorkerConfig{
		"build-a":                 {Name: "build-a"},
		"build-a=file:///srv/m":   {Name: "build-a", Endpoint: "file:///srv/m"},
		"build-a=git@host:m.git":  {Name: "build-a", Endpoint: "git@host:m.git"},
		"build-a=https://h/m.git": {Name: "build-a", Endpoint: "https://h/m.git"},
	} {
		link, err := ParseWorkerLink(value)
		require.NoError(t, err, value)
		assert.Equal(t, want, link, value)
	}
	for name, value := range map[string]string{
		"an empty endpoint":        "build-a=",
		"an empty name":            "=file:///srv/m",
		"a bare separator":         "=",
		"nothing at all":           "",
		"a name git could not use": "build..a",
		"an endpoint with no name": "https://user:hunter2@example.test/m.git",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseWorkerLink(value)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "name or name=endpoint")
			assert.NotContains(t, err.Error(), "hunter2")
		})
	}
}
