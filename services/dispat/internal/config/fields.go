package config

// The config language, written out: every object the file may hold, and every
// key it may hold it under.
//
// One table per struct, one line per field, and the line says both things the
// decoder needs — the key as a file spells it, and what writing that key does
// to the model. Nothing else in the package knows the mapping, so a field and
// its key can only ever be changed together.
//
// The tables are the drift protection too, in two halves. A model field with
// no line here has no key, so writing it in a file is an unknown key and the
// load fails loudly rather than silently ignoring what the author asked for;
// and fields_test.go walks the models' own json tags and refuses any
// disagreement in either direction, so a field added to a struct without a
// line here fails the build's tests rather than a user's release.
//
// The keys are spelled in lower case throughout, and decodeObject folds a
// file's key to find its line. The json tags they mirror are camelCase, because
// a config file is written by hand and reads better that way; folding at the
// lookup is what lets both spellings load without either being renamed.

// decodeRootConfig fills the root configuration from the settings map.
func decodeRootConfig(src map[string]any, dst *File) error {
	return decodeObject(src, "", fileFields(dst))
}

// decodePackageConfig fills a package folder's own file, whose top-level
// object is one `packages` entry.
func decodePackageConfig(src map[string]any, dst *PackageConfig) error {
	return decodeObject(src, "", packageConfigFields(dst))
}

// decodeSpaceFile fills a space folder's own file, whose top-level object is
// the space's configuration minus the path it is already sitting in.
func decodeSpaceFile(src map[string]any, dst *SpaceFile) error {
	return decodeObject(src, "", spaceFileFields(dst))
}

// fileFields is the root object: everything a monorepo states once, plus the
// repository-wide defaults for the space-shaped keys.
func fileFields(dst *File) fields {
	return fields{
		"scripts":               scriptMap(&dst.Scripts),
		"spaces":                objMap(&dst.Spaces, spaceConfigFields),
		"packages":              objMap(&dst.Packages, packageConfigFields),
		"versiongroups":         objMap(&dst.VersionGroups, versionGroupFields),
		"dependencies":          deps(&dst.Dependencies),
		"concurrency":           nums(&dst.Concurrency),
		"loglevel":              str(&dst.LogLevel),
		"logformat":             str(&dst.LogFormat),
		"changelog":             obj(&dst.Changelog, changelogFields),
		"github":                obj(&dst.GitHub, gitHubFields),
		"commit":                obj(&dst.Commit, commitFields),
		"shell":                 strs(&dst.Shell),
		"env":                   strMap(&dst.Env),
		"custom":                rawMap(&dst.Custom),
		"initials":              strMap(&dst.Initials),
		"tagformat":             str(&dst.TagFormat),
		"aliastags":             objList(&dst.AliasTags, aliasTagFields),
		"webhooks":              objList(&dst.Webhooks, webhookFields),
		"flow":                  obj(&dst.Flow, spaceFlowFields),
		"autoversion":           obj(&dst.AutoVersion, autoVersionFields),
		"isbuildwaitingpublish": flagPtr(&dst.IsBuildWaitingPublish),
		"revertonfail":          flagPtr(&dst.RevertOnFail),
		"versioning":            str(&dst.Versioning),
		"src":                   str(&dst.Src),
		"ignore":                strs(&dst.Ignore),
		"commiterrors":          str(&dst.CommitErrors),
		"nonpackagescopes":      strs(&dst.NonPackageScopes),
		"updatecheck":           flagPtr(&dst.UpdateCheck),
		"unsafedisablelock":     flag(&dst.UnsafeDisableLock),
		"run":                   obj(&dst.Run, runFields),
		"parser":                obj(&dst.Parser, parserFields),
	}
}

// spaceConfigFields is one entry of the root `spaces` map.
func spaceConfigFields(dst *SpaceConfig) fields {
	return fields{
		"path":                  pathList(&dst.Path),
		"isbuildwaitingpublish": flagPtr(&dst.IsBuildWaitingPublish),
		"revertonfail":          flagPtr(&dst.RevertOnFail),
		"flow":                  obj(&dst.Flow, spaceFlowFields),
		"tagformat":             str(&dst.TagFormat),
		"aliastags":             objList(&dst.AliasTags, aliasTagFields),
		"webhooks":              objList(&dst.Webhooks, webhookFields),
		"versioning":            str(&dst.Versioning),
		"versiongroup":          str(&dst.VersionGroup),
		"scripts":               scriptMap(&dst.Scripts),
		"autoversion":           obj(&dst.AutoVersion, autoVersionFields),
		"env":                   strMap(&dst.Env),
		"custom":                rawMap(&dst.Custom),
		"changelog":             obj(&dst.Changelog, changelogFields),
		"github":                obj(&dst.GitHub, gitHubFields),
		"src":                   str(&dst.Src),
		"concurrency":           nums(&dst.Concurrency),
		"ignore":                strs(&dst.Ignore),
		"dependencies":          deps(&dst.Dependencies),
		"packages":              objMap(&dst.Packages, packageConfigFields),
	}
}

// spaceFileFields is the top-level object of a config file sitting inside a
// space folder: spaceConfigFields without `path`, which the folder already is.
func spaceFileFields(dst *SpaceFile) fields {
	return fields{
		"isbuildwaitingpublish": flagPtr(&dst.IsBuildWaitingPublish),
		"revertonfail":          flagPtr(&dst.RevertOnFail),
		"flow":                  obj(&dst.Flow, spaceFlowFields),
		"tagformat":             str(&dst.TagFormat),
		"aliastags":             objList(&dst.AliasTags, aliasTagFields),
		"webhooks":              objList(&dst.Webhooks, webhookFields),
		"versioning":            str(&dst.Versioning),
		"versiongroup":          str(&dst.VersionGroup),
		"scripts":               scriptMap(&dst.Scripts),
		"autoversion":           obj(&dst.AutoVersion, autoVersionFields),
		"env":                   strMap(&dst.Env),
		"custom":                rawMap(&dst.Custom),
		"changelog":             obj(&dst.Changelog, changelogFields),
		"github":                obj(&dst.GitHub, gitHubFields),
		"src":                   str(&dst.Src),
		"concurrency":           nums(&dst.Concurrency),
		"ignore":                strs(&dst.Ignore),
		"dependencies":          deps(&dst.Dependencies),
		"packages":              objMap(&dst.Packages, packageConfigFields),
	}
}

// packageConfigFields is one entry of a `packages` map, at any of the four
// layers that carry one, and the top-level object of a package folder's own
// file. It holds neither `spaces` nor `packages`: a package configures one
// package, and the loader refuses those two by name before decoding, so the
// error can say why rather than reporting a bare unknown key.
//
// Its `dependencies` is the package's own provider list rather than the
// consumer-keyed object the file and a space hold, the consumer being the
// package itself.
func packageConfigFields(dst *PackageConfig) fields {
	return fields{
		"path":                  str(&dst.Path),
		"src":                   str(&dst.Src),
		"ignore":                strs(&dst.Ignore),
		"isbuildwaitingpublish": flagPtr(&dst.IsBuildWaitingPublish),
		"revertonfail":          flagPtr(&dst.RevertOnFail),
		"flow":                  obj(&dst.Flow, spaceFlowFields),
		"tagformat":             str(&dst.TagFormat),
		"aliastags":             objList(&dst.AliasTags, aliasTagFields),
		"webhooks":              objList(&dst.Webhooks, webhookFields),
		"versioning":            str(&dst.Versioning),
		"versiongroup":          str(&dst.VersionGroup),
		"scripts":               scriptMap(&dst.Scripts),
		"autoversion":           obj(&dst.AutoVersion, autoVersionFields),
		"manifestnames":         strs(&dst.ManifestNames),
		"changelog":             obj(&dst.Changelog, changelogFields),
		"github":                obj(&dst.GitHub, gitHubFields),
		"concurrency":           nums(&dst.Concurrency),
		"dependencies":          providers(&dst.Dependencies),
		"env":                   strMap(&dst.Env),
		"custom":                rawMap(&dst.Custom),
	}
}

// versionGroupFields is one entry of `versionGroups`: a declared group owns
// its versioning mode and nothing else.
func versionGroupFields(dst *VersionGroupConfig) fields {
	return fields{
		"versioning": str(&dst.Versioning),
	}
}

// spaceFlowFields is a `flow` object: what runs at which stage, keyed by stage
// or hook name with no decoration.
func spaceFlowFields(dst *SpaceFlowConfig) fields {
	return fields{
		"build":          strs(&dst.Build),
		"publish":        strs(&dst.Publish),
		"version":        strs(&dst.Version),
		"login":          strs(&dst.Login),
		"announce":       strs(&dst.Announce),
		"beforeall":      strs(&dst.BeforeAll),
		"beforeversion":  strs(&dst.BeforeVersion),
		"postversion":    strs(&dst.PostVersion),
		"beforebuild":    strs(&dst.BeforeBuild),
		"postbuild":      strs(&dst.PostBuild),
		"beforepublish":  strs(&dst.BeforePublish),
		"postpublish":    strs(&dst.PostPublish),
		"beforeannounce": strs(&dst.BeforeAnnounce),
		"postannounce":   strs(&dst.PostAnnounce),
		"onfail":         strs(&dst.OnFail),
		"onskip":         strs(&dst.OnSkip),
	}
}

// runFields is the top-level `run` object: the hooks that observe the run as a
// whole, plus the branch guard that is not a hook at all.
func runFields(dst *RunConfig) fields {
	return fields{
		"allowbranch":  strs(&dst.AllowBranch),
		"beforeall":    strs(&dst.BeforeAll),
		"postall":      strs(&dst.PostAll),
		"beforecommit": strs(&dst.BeforeCommit),
		"aftercommit":  strs(&dst.AfterCommit),
		"postcommit":   strs(&dst.PostCommit),
		"beforepush":   strs(&dst.BeforePush),
		"afterpush":    strs(&dst.AfterPush),
	}
}

// changelogFields is a `changelog` object: its own keys, plus the entry-format
// keys it shares with `github`, written side by side with no sign of the
// boundary.
func changelogFields(dst *ChangelogConfig) fields {
	return merge(fields{
		"enabled":      flagPtr(&dst.Enabled),
		"file":         str(&dst.File),
		"filetitle":    recordLines(&dst.FileTitle),
		"channels":     strs(&dst.Channels),
		"entryspacing": numPtr(&dst.EntrySpacing),
	}, entryFormatFields(&dst.EntryFormatConfig))
}

// gitHubFields is a `github` object, sharing the same entry-format keys.
func gitHubFields(dst *GitHubConfig) fields {
	return merge(fields{
		"enabled":     flagPtr(&dst.Enabled),
		"owner":       str(&dst.Owner),
		"repo":        str(&dst.Repo),
		"apiurl":      str(&dst.APIURL),
		"tokenenv":    str(&dst.TokenEnv),
		"allpackages": flagPtr(&dst.AllPackages),
		"draft":       flagPtr(&dst.Draft),
		"channels":    strs(&dst.Channels),
	}, entryFormatFields(&dst.EntryFormatConfig))
}

// entryFormatFields is the shared half of both record objects: how one release
// entry is rendered.
func entryFormatFields(dst *EntryFormatConfig) fields {
	return fields{
		"dateformat":        str(&dst.DateFormat),
		"breakingtitle":     str(&dst.BreakingTitle),
		"featurestitle":     str(&dst.FeaturesTitle),
		"fixestitle":        str(&dst.FixesTitle),
		"dependenciestitle": str(&dst.DependenciesTitle),
		"releasename":       str(&dst.ReleaseName),
		"header":            recordLines(&dst.Header),
		"footer":            recordLines(&dst.Footer),
		"authors":           obj(&dst.Authors, authorsFields),
		"dependencylink":    str(&dst.DependencyLink),
		"nochangestext":     str(&dst.NoChangesText),
		"sections":          recordSections(&dst.Sections),
		"commitrefs":        obj(&dst.CommitRefs, commitRefsFields),
	}
}

// sectionFields is one element of a `sections` list written as a full object:
// the title (or the built-in's key), the commit types a custom section claims,
// and the bump those types carry.
func sectionFields(dst *SectionConfig) fields {
	return fields{
		"title": str(&dst.Title),
		"types": strs(&dst.Types),
		"bump":  str(&dst.Bump),
	}
}

// commitRefsFields is a `commitRefs` object: whether an entry line names the
// commit behind it, how, and whether the name links anywhere.
func commitRefsFields(dst *CommitRefsConfig) fields {
	return fields{
		"placement": str(&dst.Placement),
		"format":    str(&dst.Format),
		"link":      str(&dst.Link),
	}
}

// entryLineFields is one record line written as a full object: the text, and
// the filters restricting which packages and releases it is written for.
func entryLineFields(dst *EntryLine) fields {
	return fields{
		"line":     strs(&dst.Line),
		"package":  strs(&dst.Package),
		"space":    strs(&dst.Space),
		"group":    strs(&dst.Group),
		"channels": strs(&dst.Channels),
	}
}

// authorsFields is an `authors` object: whether an entry is attributed, how,
// and to whom.
func authorsFields(dst *AuthorsConfig) fields {
	return fields{
		"placement": str(&dst.Placement),
		"format":    str(&dst.Format),
		"commits":   str(&dst.Commits),
		"include":   strs(&dst.Include),
		"exclude":   strs(&dst.Exclude),
		"title":     str(&dst.Title),
	}
}

// commitFields is the `commit` object: the single release commit at the end of
// a successful run, and what is done with it.
func commitFields(dst *CommitConfig) fields {
	return fields{
		"enabled":       flagPtr(&dst.Enabled),
		"messageformat": str(&dst.MessageFormat),
		"push":          flag(&dst.Push),
		"remote":        str(&dst.Remote),
		"force":         flagPtr(&dst.Force),
		"verify":        flagPtr(&dst.Verify),
		"include":       strs(&dst.Include),
		"name":          str(&dst.Name),
		"email":         str(&dst.Email),
	}
}

// aliasTagFields is one entry of an `aliasTags` list: an extra tag a release
// is written under.
func aliasTagFields(dst *AliasTagConfig) fields {
	return fields{
		"format":   str(&dst.Format),
		"moving":   flag(&dst.Moving),
		"channels": strs(&dst.Channels),
		"force":    flagPtr(&dst.Force),
	}
}

// webhookFields is one entry of a `webhooks` list: an endpoint notified of
// release progress.
func webhookFields(dst *WebhookConfig) fields {
	return fields{
		"name":      str(&dst.Name),
		"url":       str(&dst.URL),
		"method":    str(&dst.Method),
		"events":    strs(&dst.Events),
		"headers":   objList(&dst.Headers, webhookHeaderFields),
		"secretenv": str(&dst.SecretEnv),
		"timeout":   num(&dst.Timeout),
		"env":       str(&dst.Env),
		"format":    str(&dst.Format),
	}
}

// webhookHeaderFields is one extra request header. It is a list of name/value
// objects rather than a map so that header names keep their case, which a map
// key could not: every map key the config language holds is folded.
func webhookHeaderFields(dst *WebhookHeader) fields {
	return fields{
		"name":  str(&dst.Name),
		"value": str(&dst.Value),
	}
}

// autoVersionFields is an `autoVersion` object: the native manifest-rewriting
// policy of the version stage.
func autoVersionFields(dst *AutoVersionConfig) fields {
	return fields{
		"enabled":             flagPtr(&dst.Enabled),
		"manifests":           str(&dst.Manifests),
		"replace":             objList(&dst.Replace, autoVersionReplaceFields),
		"kinds":               strs(&dst.Kinds),
		"only":                strs(&dst.Only),
		"namematch":           str(&dst.NameMatch),
		"match":               strs(&dst.Match),
		"range":               str(&dst.Range),
		"writeversion":        flagPtr(&dst.WriteVersion),
		"synclock":            strs(&dst.SyncLock),
		"synclockconcurrency": num(&dst.SyncLockConcurrency),
	}
}

// autoVersionReplaceFields is one entry of an `autoVersion.replace` list: a
// literal find/write pair over the files its globs select.
func autoVersionReplaceFields(dst *AutoVersionReplaceConfig) fields {
	return fields{
		"files": strs(&dst.Files),
		"find":  str(&dst.Find),
		"write": str(&dst.Write),
	}
}

// parserFields is the top-level `parser` object: the commit-message parser
// options, every one of them optional.
func parserFields(dst *ParserConfig) fields {
	return fields{
		"separator":            str(&dst.Separator),
		"types":                strMap(&dst.Types),
		"stricttypes":          flag(&dst.StrictTypes),
		"quiet":                flag(&dst.Quiet),
		"lenient":              flag(&dst.Lenient),
		"maxdescriptionlength": num(&dst.MaxDescriptionLength),
		"propagation":          obj(&dst.Propagation, parserPropagationFields),
		"limits":               obj(&dst.Limits, parserLimitsFields),
		"allowedchannels":      strs(&dst.AllowedChannels),
		"messageleveltrailers": strs(&dst.MessageLevelTrailers),
		"issuetrailers":        strs(&dst.IssueTrailers),
	}
}

// parserPropagationFields is `parser.propagation`: what a unit propagates when
// it says nothing itself.
func parserPropagationFields(dst *ParserPropagationConfig) fields {
	return fields{
		"bump":         str(&dst.Bump),
		"depth":        str(&dst.Depth),
		"channeldepth": str(&dst.ChannelDepth),
		"kinds":        strs(&dst.Kinds),
		"channel":      str(&dst.Channel),
	}
}

// parserLimitsFields is `parser.limits`: the always-enforced parser bounds.
func parserLimitsFields(dst *ParserLimitsConfig) fields {
	return fields{
		"unitspermessage":   num(&dst.UnitsPerMessage),
		"scopetermsperunit": num(&dst.ScopeTermsPerUnit),
		"messagebytes":      num(&dst.MessageBytes),
	}
}
