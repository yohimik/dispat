package cli

// The environment files an invocation reads before it does anything else.
//
// A `.env` beside the terminal you type in is where a repository's local
// secrets and settings already live, so dispat reads it into its own
// environment: every script, hook and login command it runs inherits the
// process environment, and so does dispat itself, which is what lets a
// GITHUB_TOKEN or a DISPAT_ switch live in a file instead of in a shell
// profile.
//
// Two rules keep that from surprising anyone. A variable the environment
// already sets is left alone, so a value exported by a CI job always beats a
// file committed to the repository. And nothing here logs a value: an
// environment file is exactly where the secrets are.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"

	"github.com/rs/zerolog"
	"github.com/subosito/gotenv"
)

// defaultEnvFile is what an invocation reads when --env-file names nothing.
// It is looked for in the current directory, not under --root: the file
// belongs to whoever is running the command, not to the monorepo being
// released.
const defaultEnvFile = ".env"

// loadEnvFiles reads the environment files an invocation asks for and adds
// what the process environment does not already define.
//
// Named files are read in order and a later one overrides an earlier one, so
// `--env-file .env --env-file .env.ci` reads as it looks. A named file that is
// not there is an error, the way an explicit --config is: naming a file and
// silently getting none of it is worse than stopping. The default file is the
// opposite, and being absent is the ordinary case.
func loadEnvFiles(paths []string, log zerolog.Logger) error {
	explicit := len(paths) > 0
	if !explicit {
		paths = []string{defaultEnvFile}
	}
	// Every file is merged before anything is applied, so that a later file
	// overrides an earlier one rather than losing to the variable the earlier
	// one just set.
	merged := map[string]string{}
	read := make([]string, 0, len(paths))
	for _, path := range paths {
		vars, err := readEnvFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist) && !explicit:
			continue
		case err != nil:
			return err
		}
		for key, value := range vars {
			merged[key] = value
		}
		read = append(read, path)
	}
	if len(read) == 0 {
		return nil
	}

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys) // one file, one order, whatever the map iteration says
	added, kept := 0, 0
	for _, key := range keys {
		if _, set := os.LookupEnv(key); set {
			log.Trace().Str("key", key).Msg("the environment already sets this variable")
			kept++
			continue
		}
		if err := os.Setenv(key, merged[key]); err != nil {
			return fmt.Errorf("setting %s: %w", key, err)
		}
		log.Trace().Str("key", key).Msg("variable added from an environment file")
		added++
	}
	log.Debug().Strs("files", read).Int("added", added).Int("kept", kept).
		Msg("environment files read")
	return nil
}

// readEnvFile parses one environment file. Parsing is strict: a line that is
// neither blank, a comment nor an assignment is a mistake worth naming, since
// the alternative is a variable that silently never arrives.
func readEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the environment file %s: %w", path, err)
	}
	defer file.Close()
	vars, err := gotenv.StrictParse(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read the environment file %s: %w", path, err)
	}
	return vars, nil
}
