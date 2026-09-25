// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the configuration a release never gets to run.
//
// Every refusal here is a shape a real configuration reaches by a plausible
// mistake — a name bound to nothing, a path that leaves the repository, an
// enumeration spelled the way another tool spells it — and each one is
// checked through the binary rather than through the loader, because what a
// user sees is the exit code and the sentence, not the error value. A refused
// configuration must also leave the repository exactly as it found it, so
// every subtest asserts that nothing was tagged.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// refusalRepo is the one repository every refusal subtest rewrites the config
// of. A refused configuration changes nothing on disk, so one fixture serves
// the whole table and each subtest still starts from the same state.
func refusalRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.Commit("feat(core): bootstrap")
	return r
}

// refusal is one row of a refusal table: what the configuration says, and the
// sentence the reader is owed for it.
type refusal struct {
	name   string
	mutate func(*models.File)
	want   string
}

// diagnosticText is everything one refused invocation said, with the
// structured fields decoded. A refusal that happens before the configured
// logger exists is written by the bootstrap logger, which renders the
// sentence as a JSON-escaped `error` field, so the decoded field is what a
// test may assert the wording against.
func diagnosticText(res harness.RunResult) string {
	var b strings.Builder
	b.WriteString(res.Stdout)
	b.WriteString(res.Stderr)
	for _, stream := range []string{res.Stdout, res.Stderr} {
		for _, e := range harness.ParseEvents(stream) {
			b.WriteString("\n" + e.Str("error"))
			b.WriteString("\n" + e.Str("message"))
		}
	}
	return b.String()
}

// runRefusals writes each row's configuration and requires that `dispat
// status` refuses it, naming the mistake and releasing nothing.
func runRefusals(t *testing.T, r *harness.Repo, rows []refusal) {
	t.Helper()
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			cfg := libsConfig(echoBuild, 1)
			row.mutate(&cfg)
			r.WriteConfigModel(cfg)
			res := r.Status("--log-format", "json")
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, diagnosticText(res), row.want)
			assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
		})
	}
}

// TestCovConfigRefusesUnusableScriptsAndReferences: a `scripts` entry that
// binds no command, and a reference to one, are refused at whichever level
// holds them, with the level and the entry named.
func TestCovConfigRefusesUnusableScriptsAndReferences(t *testing.T) {
	r := refusalRepo(t)
	runRefusals(t, r, []refusal{
		{"nameless entry", func(c *models.File) {
			c.Scripts[""] = models.Script{"echo nameless"}
		}, "scripts contains an empty script name"},
		{"entry binding no command", func(c *models.File) {
			c.Scripts["build"] = models.Script{}
		}, `scripts["build"] is empty`},
		{"sole command blank", func(c *models.File) {
			c.Scripts["build"] = models.Script{"   "}
		}, `scripts["build"] is empty`},
		{"blank command among several", func(c *models.File) {
			c.Scripts["build"] = models.Script{"echo one", "", "echo three"}
		}, `scripts["build"][1] is empty`},
		{"space scripts entry binding no command", func(c *models.File) {
			s := c.Spaces["libs"]
			s.Scripts = map[string]models.Script{"local": {}}
			c.Spaces["libs"] = s
		}, `scripts["local"] is empty`},
		{"empty run-hook reference", func(c *models.File) {
			c.Run = &models.RunConfig{BeforeAll: []string{""}}
		}, "contains an empty script reference"},
		{"empty flow reference", func(c *models.File) {
			s := c.Spaces["libs"]
			s.Flow = &models.SpaceFlowConfig{Build: []string{""}, Publish: []string{"publish"}}
			c.Spaces["libs"] = s
		}, "contains an empty script reference"},
	})
}

// TestCovConfigRefusesInvalidRepositorySettings: the root-level settings that
// decide how a whole run behaves are each held to their own vocabulary.
func TestCovConfigRefusesInvalidRepositorySettings(t *testing.T) {
	r := refusalRepo(t)
	runRefusals(t, r, []refusal{
		{"nothing to release", func(c *models.File) {
			c.Spaces = nil
			c.Packages = nil
		}, "at least one space or package is required"},
		{"commitErrors", func(c *models.File) { c.CommitErrors = "fatal" }, "unknown commitErrors"},
		{"versioning", func(c *models.File) { c.Versioning = "semver" }, `versioning "semver" is invalid`},
		{"logLevel", func(c *models.File) { c.LogLevel = "loud" }, "unknown logLevel"},
		{"three concurrency values", func(c *models.File) { c.Concurrency = []int{1, 2, 3} },
			"concurrency accepts at most two values"},
		{"negative concurrency", func(c *models.File) { c.Concurrency = []int{-1} },
			"concurrency values must be >= 0"},
		{"nameless interpreter", func(c *models.File) { c.Shell = []string{"", "-c"} },
			"first element (the interpreter) must not be empty"},
		{"empty allowBranch pattern", func(c *models.File) {
			c.Run = &models.RunConfig{AllowBranch: []string{""}}
		}, "run.allowBranch contains an empty pattern"},
		{"absolute commit.include", func(c *models.File) {
			c.Commit = &models.CommitConfig{Enabled: models.Bool(true), Include: []string{"/etc/hosts"}}
		}, "must be a repository-relative path"},
		{"escaping commit.include", func(c *models.File) {
			c.Commit = &models.CommitConfig{Enabled: models.Bool(true), Include: []string{"../outside"}}
		}, "escapes the repository root"},
		{"initial version", func(c *models.File) {
			c.Initials = map[string]string{"core": "one point oh"}
		}, "invalid version"},
		{"tag format", func(c *models.File) { c.TagFormat = "{name}@v{major}" },
			"only available in aliasTags"},
	})

	// The log format is the one setting the table cannot carry: the rows are
	// read back as JSON events, and asking for that on the command line would
	// override the very key under test.
	t.Run("logFormat", func(t *testing.T) {
		cfg := libsConfig(echoBuild, 1)
		cfg.LogFormat = "yaml"
		r.WriteConfigModel(cfg)
		res := r.Status()
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "unknown logFormat")
		assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
	})
}

// TestCovConfigRefusesInvalidSpaceSettings: a space's own path list and
// versioning selection are held to the same rules the root settings are, with
// the space named.
func TestCovConfigRefusesInvalidSpaceSettings(t *testing.T) {
	r := refusalRepo(t)
	withSpace := func(mutate func(*models.SpaceConfig)) func(*models.File) {
		return func(c *models.File) {
			s := c.Spaces["libs"]
			mutate(&s)
			c.Spaces["libs"] = s
		}
	}
	runRefusals(t, r, []refusal{
		{"no path", withSpace(func(s *models.SpaceConfig) { s.Path = nil }), "path is required"},
		{"empty path entry", withSpace(func(s *models.SpaceConfig) {
			s.Path = models.PathList{"packages", ""}
		}), "path[1] must not be empty"},
		{"absolute path", withSpace(func(s *models.SpaceConfig) {
			s.Path = models.PathList{"/srv/packages"}
		}), "must be a repository-relative path"},
		{"escaping path", withSpace(func(s *models.SpaceConfig) {
			s.Path = models.PathList{"../elsewhere"}
		}), "escapes the repository root"},
		{"versioning and versionGroup together", withSpace(func(s *models.SpaceConfig) {
			s.Versioning = "fixed"
			s.VersionGroup = "core"
		}), "mutually exclusive"},
		{"unknown space versioning", withSpace(func(s *models.SpaceConfig) {
			s.Versioning = "calver"
		}), `unknown versioning "calver"`},
		{"unknown version group", withSpace(func(s *models.SpaceConfig) {
			s.VersionGroup = "absent"
		}), "absent"},
		{"space tag format", withSpace(func(s *models.SpaceConfig) {
			s.TagFormat = "{name}-{channel}"
		}), "contains no {version} placeholder"},
		{"space src leaves the package", withSpace(func(s *models.SpaceConfig) {
			s.Src = "../outside"
		}), "leaves the package folder"},
		{"space src is the package folder", withSpace(func(s *models.SpaceConfig) {
			s.Src = "."
		}), "is the package folder itself"},
		{"space concurrency weights", withSpace(func(s *models.SpaceConfig) {
			s.Concurrency = []int{1, 2, 3}
		}), "concurrency accepts at most two values"},
		{"negative space weight", withSpace(func(s *models.SpaceConfig) {
			s.Concurrency = []int{0, -2}
		}), "concurrency values must be >= 0"},
	})
}

// TestCovConfigRefusesInvalidRecordFormats: the changelog and GitHub record
// objects share one entry-format vocabulary, and every part of it that the
// renderer cannot carry out is refused before a release is planned.
func TestCovConfigRefusesInvalidRecordFormats(t *testing.T) {
	r := refusalRepo(t)
	runRefusals(t, r, []refusal{
		{"changelog fileTitle with no line", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{FileTitle: []models.EntryLine{{}}}
		}, "line is required"},
		{"changelog header with no line", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{Header: []models.EntryLine{{}}},
			}
		}, "line is required"},
		{"github channel with no name", func(c *models.File) {
			c.GitHub = &models.GitHubConfig{Enabled: models.Bool(false), Channels: []string{""}}
		}, "channels must not contain an empty name"},
		{"github entry format", func(c *models.File) {
			c.GitHub = &models.GitHubConfig{
				Enabled:           models.Bool(false),
				EntryFormatConfig: models.EntryFormatConfig{Footer: []models.EntryLine{{}}},
			}
		}, "line is required"},
		{"noChangesText opening a rule", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{NoChangesText: "--- nothing changed"},
			}
		}, `must not begin with "---"`},
		{"noChangesText containing a rule", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{NoChangesText: "nothing changed\n***\nreally"},
			}
		}, "horizontal rule"},
		{"commitRefs placement", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{
					CommitRefs: &models.CommitRefsConfig{Placement: "prefix"},
				},
			}
		}, "commitRefs.placement: unknown value"},
		{"authors placement", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{Authors: &models.AuthorsConfig{Placement: "footer"}},
			}
		}, "authors.placement"},
		{"authors format", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{Authors: &models.AuthorsConfig{Format: "email"}},
			}
		}, "authors.format"},
		{"authors commits", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{Authors: &models.AuthorsConfig{Commits: "merges"}},
			}
		}, "authors.commits"},
		{"authors exclude pattern", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{
					Authors: &models.AuthorsConfig{Exclude: []string{"bot@example.test", "  "}},
				},
			}
		}, "pattern must not be empty"},
		{"unknown built-in section", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{Sections: []models.SectionConfig{{Title: "Highlights"}}},
			}
		}, "is not a built-in section"},
		{"bump on a built-in section", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{
					Sections: []models.SectionConfig{{Title: "Features", Bump: "major"}},
				},
			}
		}, "bump belongs to a custom section"},
		{"built-in section listed twice", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{
					Sections: []models.SectionConfig{{Title: "Fixes"}, {Title: "fixes"}},
				},
			}
		}, "is listed twice"},
		{"section with no title", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{
					Sections: []models.SectionConfig{{Types: []string{"perf"}}},
				},
			}
		}, "title is required"},
		{"section bump value", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{
					Sections: []models.SectionConfig{{Title: "Performance", Types: []string{"perf"}, Bump: "huge"}},
				},
			}
		}, "bump: unknown value"},
		{"section type with no name", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{
					Sections: []models.SectionConfig{{Title: "Performance", Types: []string{"perf", " "}}},
				},
			}
		}, "a commit type must not be empty"},
		{"type claimed twice", func(c *models.File) {
			c.Changelog = &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{
					Sections: []models.SectionConfig{
						{Title: "Performance", Types: []string{"perf"}},
						{Title: "Speed", Types: []string{"perf"}},
					},
				},
			}
		}, "is already claimed by"},
		{"package record object", func(c *models.File) {
			c.Packages = map[string]models.PackageConfig{"core": {
				Path:      "packages/core",
				Changelog: &models.ChangelogConfig{Channels: []string{""}},
			}}
		}, "channels must not contain an empty name"},
		{"space package record object", func(c *models.File) {
			s := c.Spaces["libs"]
			s.Packages = map[string]models.PackageConfig{"core": {
				Changelog: &models.ChangelogConfig{Channels: []string{""}},
			}}
			c.Spaces["libs"] = s
		}, "channels must not contain an empty name"},
	})
}

// TestCovConfigRefusesInvalidAutoVersionRules: the native manifest
// reconciliation reads several vocabularies of its own, and a rule it cannot
// apply is refused rather than quietly writing nothing.
func TestCovConfigRefusesInvalidAutoVersionRules(t *testing.T) {
	r := refusalRepo(t)
	av := func(a models.AutoVersionConfig) func(*models.File) {
		return func(c *models.File) {
			s := c.Spaces["libs"]
			s.AutoVersion = &a
			c.Spaces["libs"] = s
		}
	}
	runRefusals(t, r, []refusal{
		{"manifests", av(models.AutoVersionConfig{Manifests: "some"}), "manifests: unknown value"},
		{"replace rule with no files", av(models.AutoVersionConfig{
			Replace: []models.AutoVersionReplaceConfig{{Find: "x", Write: "y"}},
		}), "files is required"},
		{"replace rule with an empty glob", av(models.AutoVersionConfig{
			Replace: []models.AutoVersionReplaceConfig{{Files: []string{""}, Find: "x", Write: "y"}},
		}), "files: empty glob"},
		{"replace rule with a broken glob", av(models.AutoVersionConfig{
			Replace: []models.AutoVersionReplaceConfig{{Files: []string{"[a-"}, Find: "x", Write: "y"}},
		}), "invalid pattern"},
		{"replace rule with no find", av(models.AutoVersionConfig{
			Replace: []models.AutoVersionReplaceConfig{{Files: []string{"*.txt"}, Write: "y"}},
		}), "find is required"},
		{"replace rule with no write", av(models.AutoVersionConfig{
			Replace: []models.AutoVersionReplaceConfig{{Files: []string{"*.txt"}, Find: "x"}},
		}), "write is required"},
		{"dependency kind", av(models.AutoVersionConfig{Kinds: []string{"buildDependencies"}}), "kinds:"},
		{"match pattern", av(models.AutoVersionConfig{Match: []string{"[a-"}}), "match: invalid pattern"},
		{"nameMatch", av(models.AutoVersionConfig{NameMatch: "fuzzy"}), "nameMatch: unknown value"},
		{"syncLockConcurrency", av(models.AutoVersionConfig{SyncLockConcurrency: -2}),
			"syncLockConcurrency must be >= 0"},
	})
}

// TestCovConfigRefusesInvalidParserSettings: the parser block is dispat's
// view of the CCME configuration, and a value the parser itself would refuse
// is refused while the configuration is still being loaded.
func TestCovConfigRefusesInvalidParserSettings(t *testing.T) {
	r := refusalRepo(t)
	parser := func(p models.ParserConfig) func(*models.File) {
		return func(c *models.File) { c.Parser = &p }
	}
	runRefusals(t, r, []refusal{
		{"propagation bump", parser(models.ParserConfig{
			Propagation: &models.ParserPropagationConfig{Bump: "huge"},
		}), "propagation.bump: unknown value"},
		{"propagation depth", parser(models.ParserConfig{
			Propagation: &models.ParserPropagationConfig{Depth: "deep"},
		}), "propagation.depth"},
		{"propagation channelDepth", parser(models.ParserConfig{
			Propagation: &models.ParserPropagationConfig{ChannelDepth: "-3"},
		}), "propagation.channelDepth"},
		{"propagation kinds", parser(models.ParserConfig{
			Propagation: &models.ParserPropagationConfig{Kinds: []string{"buildDependencies"}},
		}), "propagation.kinds: unknown kind"},
		// The wildcard is spelled "*", the scope-set selector of §5.2, and
		// "all" is a plausible guess the loader does not take. The case is
		// here so the documentation cannot drift back into offering it.
		{"propagation kinds spelled as a word", parser(models.ParserConfig{
			Propagation: &models.ParserPropagationConfig{Kinds: []string{"all"}},
		}), `propagation.kinds: unknown kind "all"`},
		{"separator the parser refuses", parser(models.ParserConfig{Separator: "-"}),
			"separator"},
	})
}

// TestCovConfigRefusesInvalidAliasTags: an alias is only ever written, so the
// rules it is held to are its own — and the strictest of them is that an
// alias must never be readable back as a release tag.
func TestCovConfigRefusesInvalidAliasTags(t *testing.T) {
	r := refusalRepo(t)
	alias := func(a models.AliasTagConfig) func(*models.File) {
		return func(c *models.File) { c.AliasTags = []models.AliasTagConfig{a} }
	}
	runRefusals(t, r, []refusal{
		{"no format", alias(models.AliasTagConfig{Moving: true}), "format is required"},
		{"format naming no version part", alias(models.AliasTagConfig{Format: "{name}-latest"}),
			"names no part of the version"},
		{"moving alias pinned", alias(models.AliasTagConfig{Format: "{name}@v{major}", Moving: true, Force: models.Bool(false)}),
			"a moving alias cannot set force: false"},
		{"channel with no name", alias(models.AliasTagConfig{Format: "{name}@v{major}", Channels: []string{""}}),
			"channels must not contain an empty name"},
		{"alias readable as a release tag", alias(models.AliasTagConfig{Format: "{name}@{version}"}),
			"would be read back as a release tag"},
		{"space alias tag", func(c *models.File) {
			s := c.Spaces["libs"]
			s.AliasTags = []models.AliasTagConfig{{Format: ""}}
			c.Spaces["libs"] = s
		}, "format is required"},
	})
}

// TestCovConfigRefusesInvalidPackageDeclarations: what a package declares
// about itself — where its sources are, which manifests name it, what it
// depends on — is held against the packages discovery actually found.
func TestCovConfigRefusesInvalidPackageDeclarations(t *testing.T) {
	r := refusalRepo(t)
	r.SeedPackage("packages", "utils")
	r.Commit("feat(utils): a second package")
	runRefusals(t, r, []refusal{
		{"absolute source directory", func(c *models.File) {
			c.Packages = map[string]models.PackageConfig{"core": {Src: r.Path("packages/core")}}
		}, "must be a path relative to the package folder"},
		{"negative package concurrency", func(c *models.File) {
			c.Packages = map[string]models.PackageConfig{"core": {Concurrency: []int{-1}}}
		}, "concurrency values must be >= 0"},
		{"invalid package environment name", func(c *models.File) {
			c.Packages = map[string]models.PackageConfig{"core": {Env: map[string]string{"BAD=NAME": "value"}}}
		}, "env:"},
		{"invalid package webhook", func(c *models.File) {
			c.Packages = map[string]models.PackageConfig{"core": {Webhooks: []models.WebhookConfig{{URL: "ftp://example.test/hook"}}}}
		}, "must use http or https"},
		{"src naming a file", func(c *models.File) {
			c.Packages = map[string]models.PackageConfig{"core": {Src: "main.txt"}}
		}, "names a file, want a folder"},
		{"manifest name with no text", func(c *models.File) {
			c.Packages = map[string]models.PackageConfig{"core": {ManifestNames: []string{""}}}
		}, "manifestNames: empty name"},
		{"manifest name claimed twice", func(c *models.File) {
			c.Packages = map[string]models.PackageConfig{
				"core":  {ManifestNames: []string{"@acme/shared"}},
				"utils": {ManifestNames: []string{"@acme/shared"}},
			}
		}, "identifies one package"},
		{"unknown consumer", func(c *models.File) {
			c.Dependencies = models.Dependencies{{Consumer: "absent", Provider: "core"}}
		}, "unknown consumer package"},
		{"unknown provider", func(c *models.File) {
			c.Dependencies = models.Dependencies{{Consumer: "core", Provider: "absent"}}
		}, "absent"},
	})
}
