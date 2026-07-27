// Package config loads and validates the monorepo configuration file (via
// viper) and discovers the packages living inside the configured spaces.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/yohimik/monorel/internal/model"
)

// File mirrors the configuration at the monorepo root. Viper infers the
// format from the file extension (yaml, json, toml, ...); note that viper
// treats keys case-insensitively and lowercases map keys, so script and space
// names are matched case-insensitively.
type File struct {
	Scripts      map[string]string      `mapstructure:"scripts"`
	Spaces       map[string]SpaceConfig `mapstructure:"spaces"`
	Dependencies []DependencyConfig     `mapstructure:"dependencies"`
	Concurrency  int                    `mapstructure:"concurrency"`
	LogLevel     string                 `mapstructure:"logLevel"`
}

// SpaceConfig is the raw configuration of one space.
type SpaceConfig struct {
	Path                  string `mapstructure:"path"`
	IsBuildWaitingPublish bool   `mapstructure:"isBuildWaitingPublish"`
	BuildScript           string `mapstructure:"buildScript"`
	PublishScript         string `mapstructure:"publishScript"`
}

// DependencyConfig is one consumer -> provider relation.
type DependencyConfig struct {
	Consumer string `mapstructure:"consumer"`
	Provider string `mapstructure:"provider"`
}

var validLevels = map[string]bool{
	"pretty": true, "trace": true, "debug": true,
	"info": true, "warn": true, "error": true,
}

// Load reads and validates the configuration file. When flags is non-nil the
// "concurrency" and "log-level" flags are bound through viper, so explicitly
// set flags override file values (and file values override flag defaults).
// Defaults applied afterwards: concurrency 0 means the number of CPUs,
// logLevel defaults to "pretty".
func Load(path string, flags *pflag.FlagSet) (*File, error) {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %w", path, err)
	}
	if flags != nil {
		for key, flagName := range map[string]string{
			"concurrency": "concurrency",
			"logLevel":    "log-level",
		} {
			if f := flags.Lookup(flagName); f != nil {
				if err := v.BindPFlag(key, f); err != nil {
					return nil, fmt.Errorf("config: binding flag %s: %w", flagName, err)
				}
			}
		}
	}

	var cfg File
	// UnmarshalExact rejects unknown keys, catching config typos early.
	if err := v.UnmarshalExact(&cfg); err != nil {
		return nil, fmt.Errorf("config: invalid format in %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}

// script resolves a script reference case-insensitively, because viper
// lowercases the keys of the scripts map.
func (c *File) script(ref string) (string, bool) {
	s, ok := c.Scripts[strings.ToLower(ref)]
	return s, ok
}

func (c *File) validate() error {
	if len(c.Spaces) == 0 {
		return errors.New("at least one space is required")
	}
	if c.Concurrency < 0 {
		return fmt.Errorf("concurrency must be >= 0, got %d", c.Concurrency)
	}
	if c.Concurrency == 0 {
		c.Concurrency = runtime.NumCPU()
	}
	if c.LogLevel == "" {
		c.LogLevel = "pretty"
	}
	if !validLevels[c.LogLevel] {
		return fmt.Errorf("unknown logLevel %q (want pretty, trace, debug, info, warn or error)", c.LogLevel)
	}
	for name, s := range c.Spaces {
		if s.Path == "" {
			return fmt.Errorf("space %q: path is required", name)
		}
		if s.BuildScript == "" || s.PublishScript == "" {
			return fmt.Errorf("space %q: buildScript and publishScript are required", name)
		}
		for _, ref := range []string{s.BuildScript, s.PublishScript} {
			if _, ok := c.script(ref); !ok {
				return fmt.Errorf("space %q references unknown script %q", name, ref)
			}
		}
	}
	for i, d := range c.Dependencies {
		if d.Consumer == "" || d.Provider == "" {
			return fmt.Errorf("dependencies[%d]: consumer and provider are required", i)
		}
		if d.Consumer == d.Provider {
			return fmt.Errorf("dependencies[%d]: package %q cannot depend on itself", i, d.Consumer)
		}
	}
	return nil
}

// Discover walks every space folder and returns the packages found inside,
// plus the validated dependency edges. Every direct sub-folder of a space is a
// package named after the folder; names must be unique across all spaces.
func (c *File) Discover(root string) ([]*model.Package, []model.Dependency, error) {
	spaceNames := make([]string, 0, len(c.Spaces))
	for n := range c.Spaces {
		spaceNames = append(spaceNames, n)
	}
	sort.Strings(spaceNames) // deterministic discovery order

	var pkgs []*model.Package
	owner := make(map[string]string) // package name -> space name
	for _, sn := range spaceNames {
		sc := c.Spaces[sn]
		build, _ := c.script(sc.BuildScript)
		publish, _ := c.script(sc.PublishScript)
		space := &model.Space{
			Name:              sn,
			Path:              sc.Path,
			BuildWaitsPublish: sc.IsBuildWaitingPublish,
			BuildScript:       build,
			PublishScript:     publish,
		}
		dir := filepath.Join(root, sc.Path)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("config: space %q: %w", sn, err)
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := e.Name()
			if prev, dup := owner[name]; dup {
				return nil, nil, fmt.Errorf(
					"config: package %q exists in both space %q and space %q; package names must be unique",
					name, prev, sn)
			}
			owner[name] = sn
			pkgs = append(pkgs, &model.Package{
				Name:  name,
				Dir:   filepath.Join(dir, name),
				Space: space,
			})
		}
	}

	deps := make([]model.Dependency, 0, len(c.Dependencies))
	for i, d := range c.Dependencies {
		if _, ok := owner[d.Consumer]; !ok {
			return nil, nil, fmt.Errorf("config: dependencies[%d]: unknown consumer package %q", i, d.Consumer)
		}
		if _, ok := owner[d.Provider]; !ok {
			return nil, nil, fmt.Errorf("config: dependencies[%d]: unknown provider package %q", i, d.Provider)
		}
		deps = append(deps, model.Dependency{Consumer: d.Consumer, Provider: d.Provider})
	}
	return pkgs, deps, nil
}
