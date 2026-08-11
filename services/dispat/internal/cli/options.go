package cli

import (
	"github.com/spf13/pflag"

	"github.com/yohimik/dispat/services/dispat/internal/app"
)

// options holds a pointer to every declared flag's value. Grouping them
// keeps Run about dispatch rather than about forty local variables, and it
// is the list the command table in usage.go names by flag name — the two
// together are the whole flag surface.
type options struct {
	// global
	root, cfgName         *string
	logLevel, logFormat   *string
	quietParser           *bool
	showVersion, showHelp *bool

	// run
	onError, since *string
	consumers      *bool

	// selection, shared by every package-selecting command
	pkgFilter, spaceFilter *[]string

	// init
	initFormat *string

	// compute
	computeWrite, computeInteractive, computeCheck *bool

	// commit
	commitTag, commitPush                                *bool
	commitName, commitEmail, commitRemote, commitMessage *string
	commitInclude                                        *[]string

	// github
	ghOwner, ghRepo, ghAPIURL, ghTokenEnv, ghTarget *string

	// changelog
	clFile, clTitle, clDateFormat *string

	// autoversion
	avRange, avManifests                    *string
	avMatch                                 *[]string
	avNoReplace, avWriteVersion, avSyncLock *bool

	// scanner, writer, replacer
	scanRootOnly     *bool
	wrSetVersion     *string
	wrSet, wrReplace *[]string
	rpSub            *[]string
	strict           *bool
}

// declareFlags declares every flag on fs and records the pointers. It is
// separate from Run so that the flag surface can be built — and checked
// against the command table — without running a command.
func declareFlags(fs *pflag.FlagSet) *options {
	o := &options{}
	o.root = fs.String("root", ".", "monorepo root folder")
	o.cfgName = fs.String("config", "dispat.json",
		"config file name, relative to --root; when not set, the first of dispat.json, dispat.yaml, dispat.yml, dispat.toml that exists")
	fs.IntSlice("concurrency", nil, "override the configured concurrency: one value for both stages, or build,publish (e.g. 4,2); dispat run uses the build value")
	o.logLevel = fs.String("log-level", "", "override the configured logLevel (trace, debug, info, warn, error)")
	o.logFormat = fs.String("log-format", "", "override the configured logFormat (pretty, json)")
	o.quietParser = fs.Bool("quiet-parser", false,
		"hide the commit-message parser's diagnostics; --quiet-parser=false shows them again when parser.quiet is set")
	o.onError = fs.String("on-error", app.OnErrorSkip,
		"what a failing script does to the failed package's dependents (skip or continue)")
	o.since = fs.String("since", "",
		"select the packages the commits since the git revision address (scopes first, changed files for scopeless commits; e.g. HEAD~1, origin/main, a tag; 'all' selects every package) instead of the release window")
	o.consumers = fs.Bool("consumers", false,
		"additionally run every package that transitively depends on a selected one, so downstream consumers are re-run with the change")
	o.pkgFilter = fs.StringSliceP("package", "p", nil,
		"narrow to the named packages (repeatable and comma-separated; '*' globs, so -p '*' is every package and -p '@acme/*' a prefix); without it, the package folder the command was invoked from")
	o.spaceFilter = fs.StringSliceP("space", "s", nil,
		"narrow to every package of the named spaces (repeatable, comma-separated, '*' globs); a standalone package belongs to no space, so --package is the only way to name one")
	o.initFormat = fs.String("format", "json",
		"config file format (json, yaml or toml)")
	o.computeWrite = fs.Bool("write", false,
		"apply every suggestion to the config file")
	o.computeInteractive = fs.BoolP("interactive", "i", false,
		"confirm each suggestion before applying it")
	o.computeCheck = fs.Bool("check", false,
		"report only and exit 1 when suggestions exist (CI gate)")
	o.commitTag = fs.Bool("tag", false,
		"also create the annotated release tag at the resulting commit; an identical existing tag is skipped")
	o.commitPush = fs.Bool("push", false,
		"push the branch, and with --tag the tag(s); tags already on the remote are skipped")
	o.commitName = fs.String("name", "",
		"override the commit.name committer identity")
	o.commitEmail = fs.String("email", "",
		"override the commit.email committer identity")
	o.commitRemote = fs.String("remote", "",
		"override the commit.remote push target")
	o.commitMessage = fs.String("message-format", "",
		"override the commit.messageFormat template ({tags}, {packages})")
	o.commitInclude = fs.StringSlice("include", nil,
		"override the commit.include extra staged paths")
	o.ghOwner = fs.String("owner", "",
		"override the github.owner repository owner")
	o.ghRepo = fs.String("repo", "",
		"override the github.repo repository name")
	o.ghAPIURL = fs.String("api-url", "",
		"override the github.apiUrl API endpoint (for GitHub Enterprise)")
	o.ghTokenEnv = fs.String("token-env", "",
		"override the github.tokenEnv variable the token is read from")
	o.ghTarget = fs.String("target", "",
		"create the tag at this commit or branch (target_commitish); only safe once the commit is on the remote")
	o.clFile = fs.String("file", "",
		"override the changelog.file name")
	o.clTitle = fs.String("title", "",
		"override the changelog.title first line")
	o.clDateFormat = fs.String("date-format", "",
		"override the changelog.dateFormat entry date layout")
	o.avRange = fs.String("range", "",
		"override the autoVersion.range write policy")
	o.avMatch = fs.StringSlice("match", nil,
		"override the autoVersion.match range globs")
	o.avManifests = fs.String("manifests", "",
		"override autoVersion.manifests (root, all or none)")
	o.avNoReplace = fs.Bool("no-replace", false,
		"skip the autoVersion.replace rules for this invocation")
	o.avWriteVersion = fs.Bool("write-version", true,
		"override autoVersion.writeVersion")
	o.avSyncLock = fs.Bool("sync-lock", true,
		"run the space's syncLock scripts for changed packages")
	o.scanRootOnly = fs.Bool("root-only", false,
		"read only the manifests sitting directly in the folder, without descending")
	o.wrSetVersion = fs.String("set-version", "",
		"rewrite each manifest's own version field to this version")
	o.wrSet = fs.StringArray("set", nil,
		"set one dependency's declared range, [kind:]name=range (repeatable)")
	o.wrReplace = fs.StringArray("replace", nil,
		"point a dependency at a local folder, name=path; an empty path removes the redirect (repeatable)")
	o.rpSub = fs.StringArray("sub", nil,
		"replace literal text in the named files, find=>write (repeatable, applied in order)")
	o.strict = fs.Bool("strict", false,
		"exit 1 on a manifest that failed to parse, an edit the manifest does not declare, or a --sub that matched nothing")
	o.showVersion = fs.Bool("version", false, "print the dispat version and exit")
	// Declaring help is what makes it a flag rather than pflag's own
	// interception, which fires during Parse — before the command word has
	// been read — and is why help used to be the whole program's, whatever
	// command was asked for.
	o.showHelp = fs.BoolP("help", "h", false,
		"print help for the command and exit")
	return o
}
