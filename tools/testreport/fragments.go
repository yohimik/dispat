package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	fragmentVerb              = "fragments"
	fragmentCollectMode       = "collect"
	fragmentMergeMode         = "merge"
	fragmentUnitLane          = "unit"
	fragmentIntegrationLane   = "integration"
	fragmentRaceLane          = "race"
	fragmentChecksLane        = "checks"
	fragmentManifestName      = "manifest.json"
	fragmentCoverageName      = "coverage"
	fragmentTestLogName       = "testlog"
	fragmentArtifactPrefix    = "test-fragment-"
	fragmentProfileSuffix     = ".out"
	fragmentStampSuffix       = ".commit"
	fragmentLogSuffix         = ".json"
	fragmentSchemaVersion     = 1
	fragmentCommitHexLength   = 40
	fragmentFileMode          = 0o644
	fragmentDirectoryMode     = 0o755
	fragmentHashFormat        = "%x"
	fragmentFlagError         = "fragments requires -mode collect|merge, -commit <40 lowercase hex>, and -out <fresh-dir>"
	fragmentLaneError         = "fragments collect requires -lane unit|integration|race|checks"
	fragmentMergeLaneError    = "fragments merge does not take -lane"
	fragmentUnexpectedFormat  = "unexpected %s in %s"
	fragmentMissingFormat     = "missing %s in %s"
	fragmentStampFormat       = "%s was measured at %q, want %q"
	fragmentDigestFormat      = "%s digest or size differs from its manifest"
	fragmentLogFailedFormat   = "%s contains a failed test or package"
	fragmentLogOpenFormat     = "%s has unfinished started tests or packages"
	fragmentLogNoPassFormat   = "%s contains no passed test or package"
	fragmentLogPackageFormat  = "%s contains unrelated package %s"
	fragmentPackagePrefix     = "github.com/yohimik/dispat/"
	fragmentCCMESuite         = "ccme"
	fragmentConfigSuite       = "config"
	fragmentManifestSuite     = "manifest"
	fragmentModelsSuite       = "models"
	fragmentScannerSuite      = "scanner"
	fragmentWriterSuite       = "writer"
	fragmentToolsSuite        = "tools"
	fragmentDispatSuite       = "dispat"
	fragmentProfileKind       = "profile"
	fragmentStampKind         = "stamp"
	fragmentLogKind           = "log"
	fragmentInputDefault      = "coverage-fragments"
	fragmentReadErrorFormat   = "%s could not be read: %w"
	fragmentProfileError      = "%s is not a coverage profile: %w"
	fragmentProfileEmpty      = "%s has no coverage blocks"
	fragmentJSONError         = "%s is not a complete Go test JSON stream: %w"
	fragmentEventError        = "%s has an event without package or action"
	fragmentActionError       = "%s contains unexpected Go test action %q"
	fragmentChildCountError   = "fragment input has %d child entries, want %d"
	fragmentDuplicateError    = "duplicate fragment file %s"
	fragmentManifestError     = "%s is not a fragment manifest: %w"
	fragmentManifestTrailing  = "%s has trailing manifest data"
	fragmentManifestMismatch  = "%s manifest has wrong schema, source, lane or full flag"
	fragmentInputDirectory    = "fragment input directory"
	fragmentCoverageDirectory = "coverage directory"
	fragmentInputLayout       = "fragment input"
	fragmentLayout            = "fragment layout"
	fragmentTripletLabel      = " complete profile/stamp/log triplet"
	fragmentManifestIndent    = "  "
	fragmentPackageSeparator  = "/"
	fragmentIntegrationPath   = "tests/integration"
	fragmentDispatPath        = "services/dispat"
	fragmentLibraryPath       = "pkg/"
	fragmentActionStart       = "start"
	fragmentActionRun         = "run"
	fragmentActionPass        = "pass"
	fragmentActionSkip        = "skip"
	fragmentActionFail        = "fail"
	fragmentActionOutput      = "output"
	fragmentActionPause       = "pause"
	fragmentActionContinue    = "cont"
	fragmentActionBench       = "bench"
	fragmentModeFlag          = "mode"
	fragmentLaneFlag          = "lane"
	fragmentCommitFlag        = "commit"
	fragmentCoverageFlag      = "coverage"
	fragmentInputFlag         = "input"
	fragmentOutFlag           = "out"
	fragmentFullFlag          = "full"
	fragmentModeHelp          = "collect or merge"
	fragmentLaneHelp          = "evidence lane for collection"
	fragmentCommitHelp        = "tested 40-hex commit"
	fragmentCoverageHelp      = "source coverage directory"
	fragmentInputHelp         = "downloaded fragment directory"
	fragmentOutHelp           = "fresh output directory"
	fragmentFullHelp          = "require every file in each lane"
	fragmentEmptyValue        = ""
	fragmentEmptyCount        = 0
	fragmentManifestOnlyCount = 1
	fragmentWithCoverageCount = 2
	fragmentHexDigitZero      = '0'
	fragmentHexDigitNine      = '9'
	fragmentHexDigitA         = 'a'
	fragmentHexDigitF         = 'f'
	fragmentManifestNewline   = '\n'
	fragmentDefaultFull       = false
)

var fragmentUnitSuites = []string{
	fragmentCCMESuite, fragmentConfigSuite, fragmentManifestSuite, fragmentModelsSuite,
	fragmentScannerSuite, fragmentWriterSuite, fragmentToolsSuite, fragmentDispatSuite,
}

var fragmentRaceSuites = []string{
	fragmentCCMESuite, fragmentConfigSuite, fragmentManifestSuite, fragmentModelsSuite,
	fragmentScannerSuite, fragmentWriterSuite, fragmentToolsSuite, fragmentDispatSuite,
	fragmentIntegrationLane,
}

var fragmentLanes = []string{
	fragmentUnitLane, fragmentIntegrationLane, fragmentRaceLane, fragmentChecksLane,
}

type fragmentOptions struct {
	Mode     string
	Lane     string
	Commit   string
	Coverage string
	Input    string
	Out      string
	Full     bool
}

type fragmentReadOptions struct {
	Root   string
	Lane   string
	Commit string
	Full   bool
}

type fragmentFileCheck struct {
	Path   string
	Kind   string
	Commit string
	Data   []byte
}

type fragmentFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type fragmentManifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	Source        string         `json:"source"`
	Lane          string         `json:"lane"`
	Full          bool           `json:"full"`
	Files         []fragmentFile `json:"files"`
}

func fragments(args []string) error {
	fs := flag.NewFlagSet(fragmentVerb, flag.ContinueOnError)
	options := fragmentOptions{}
	fs.StringVar(&options.Mode, fragmentModeFlag, fragmentEmptyValue, fragmentModeHelp)
	fs.StringVar(&options.Lane, fragmentLaneFlag, fragmentEmptyValue, fragmentLaneHelp)
	fs.StringVar(&options.Commit, fragmentCommitFlag, fragmentEmptyValue, fragmentCommitHelp)
	fs.StringVar(&options.Coverage, fragmentCoverageFlag, fragmentCoverageName, fragmentCoverageHelp)
	fs.StringVar(&options.Input, fragmentInputFlag, fragmentInputDefault, fragmentInputHelp)
	fs.StringVar(&options.Out, fragmentOutFlag, fragmentEmptyValue, fragmentOutHelp)
	fs.BoolVar(&options.Full, fragmentFullFlag, fragmentDefaultFull, fragmentFullHelp)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != fragmentEmptyCount || !validFragmentCommit(options.Commit) ||
		options.Out == fragmentEmptyValue {
		return errors.New(fragmentFlagError)
	}
	switch options.Mode {
	case fragmentCollectMode:
		if !slices.Contains(fragmentLanes, options.Lane) {
			return errors.New(fragmentLaneError)
		}
		return collectFragment(options)
	case fragmentMergeMode:
		if options.Lane != fragmentEmptyValue {
			return errors.New(fragmentMergeLaneError)
		}
		return mergeFragments(options)
	default:
		return errors.New(fragmentFlagError)
	}
}

func validFragmentCommit(commit string) bool {
	if len(commit) != fragmentCommitHexLength {
		return false
	}
	for _, char := range commit {
		if char < fragmentHexDigitZero || char > fragmentHexDigitNine &&
			(char < fragmentHexDigitA || char > fragmentHexDigitF) {
			return false
		}
	}
	return true
}

// fragmentInventory names every file a lane can certify. A unit or
// integration suite is indivisible: profile, stamp, and log travel together.
func fragmentInventory(lane string) map[string]string {
	want := make(map[string]string)
	addSuite := func(name string) {
		want[name+fragmentProfileSuffix] = fragmentProfileKind
		want[name+fragmentStampSuffix] = fragmentStampKind
		want[filepath.Join(fragmentTestLogName, name+fragmentLogSuffix)] = fragmentLogKind
	}
	switch lane {
	case fragmentUnitLane:
		for _, name := range fragmentUnitSuites {
			addSuite(name)
		}
	case fragmentIntegrationLane:
		addSuite(fragmentIntegrationLane)
	case fragmentRaceLane:
		for _, name := range fragmentRaceSuites {
			want[filepath.Join(fragmentTestLogName, name+raceSuffix+fragmentLogSuffix)] = fragmentLogKind
		}
	}
	return want
}

func collectFragment(options fragmentOptions) error {
	files, err := readFragmentFiles(fragmentReadOptions{
		Root: options.Coverage, Lane: options.Lane, Commit: options.Commit, Full: options.Full,
	})
	if err != nil {
		return err
	}
	manifest := newFragmentManifest(options, files)
	return writeFragment(options.Out, manifest, files)
}

func newFragmentManifest(options fragmentOptions, files map[string][]byte) fragmentManifest {
	manifest := fragmentManifest{
		SchemaVersion: fragmentSchemaVersion, Source: options.Commit,
		Lane: options.Lane, Full: options.Full,
		Files: make([]fragmentFile, fragmentEmptyCount, len(files)),
	}
	for path, data := range files {
		digest := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, fragmentFile{
			Path: path, SHA256: fmt.Sprintf(fragmentHashFormat, digest), Size: int64(len(data)),
		})
	}
	slices.SortFunc(manifest.Files, func(a, b fragmentFile) int { return strings.Compare(a.Path, b.Path) })
	return manifest
}

func readFragmentFiles(options fragmentReadOptions) (map[string][]byte, error) {
	want := fragmentInventory(options.Lane)
	paths, err := fragmentWalk(options.Root, !options.Full || options.Lane == fragmentChecksLane)
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		kind, allowed := want[path]
		if !allowed {
			return nil, fmt.Errorf(fragmentUnexpectedFormat, path, options.Root)
		}
		data, err := os.ReadFile(filepath.Join(options.Root, path))
		if err != nil {
			return nil, fmt.Errorf(fragmentReadErrorFormat, path, err)
		}
		if err := validateFragmentFile(fragmentFileCheck{
			Path: path, Kind: kind, Data: data, Commit: options.Commit,
		}); err != nil {
			return nil, err
		}
		files[path] = data
	}
	if err := checkFragmentInventory(options.Lane, files, options.Full); err != nil {
		return nil, err
	}
	return files, nil
}

func fragmentWalk(root string, allowAbsent bool) ([]string, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) && allowAbsent {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf(fragmentUnexpectedFormat, root, fragmentCoverageDirectory)
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel == fragmentTestLogName {
				return nil
			}
			return fmt.Errorf(fragmentUnexpectedFormat, rel, root)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf(fragmentUnexpectedFormat, rel, root)
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

func checkFragmentInventory(lane string, files map[string][]byte, full bool) error {
	want := fragmentInventory(lane)
	for path := range files {
		if _, ok := want[path]; !ok {
			return fmt.Errorf(fragmentUnexpectedFormat, path, lane)
		}
	}
	if lane == fragmentUnitLane || lane == fragmentIntegrationLane {
		suites := fragmentUnitSuites
		if lane == fragmentIntegrationLane {
			suites = []string{fragmentIntegrationLane}
		}
		for _, suite := range suites {
			parts := []string{suite + fragmentProfileSuffix, suite + fragmentStampSuffix,
				filepath.Join(fragmentTestLogName, suite+fragmentLogSuffix)}
			present := 0
			for _, path := range parts {
				if _, ok := files[path]; ok {
					present++
				}
			}
			if present != fragmentEmptyCount && present != len(parts) || full && present != len(parts) {
				return fmt.Errorf(fragmentMissingFormat, suite+fragmentTripletLabel, lane)
			}
		}
	}
	if full {
		for path := range want {
			if _, ok := files[path]; !ok {
				return fmt.Errorf(fragmentMissingFormat, path, lane)
			}
		}
	}
	return nil
}

func validateFragmentFile(check fragmentFileCheck) error {
	switch check.Kind {
	case fragmentStampKind:
		if got := strings.TrimSpace(string(check.Data)); got != check.Commit {
			return fmt.Errorf(fragmentStampFormat, check.Path, got, check.Commit)
		}
	case fragmentProfileKind:
		profile := newCoverage()
		if err := profile.add(bytes.NewReader(check.Data)); err != nil {
			return fmt.Errorf(fragmentProfileError, check.Path, err)
		}
		if len(profile.statements) == fragmentEmptyCount {
			return fmt.Errorf(fragmentProfileEmpty, check.Path)
		}
	case fragmentLogKind:
		return validateFragmentLog(check.Path, check.Data)
	}
	return nil
}

// validateFragmentLog requires a completed, successful go test stream. The
// ordinary report parser counts results but intentionally permits partial
// diagnostic logs; a CI fragment is an acceptance artifact and cannot.
func validateFragmentLog(path string, data []byte) error {
	// The sharded merger keeps every worker's package start but emits one
	// aggregate package terminal. A terminal closes all starts for that package.
	packages := map[string]bool{}
	tests := map[string]int{}
	passed := fragmentEmptyCount
	expectedPackage := fragmentExpectedPackage(path)
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var record event
		if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf(fragmentJSONError, path, err)
		}
		if record.Package == fragmentEmptyValue || record.Action == fragmentEmptyValue {
			return fmt.Errorf(fragmentEventError, path)
		}
		if record.Package != expectedPackage &&
			!strings.HasPrefix(record.Package, expectedPackage+fragmentPackageSeparator) {
			return fmt.Errorf(fragmentLogPackageFormat, path, record.Package)
		}
		key := failedKey(record.Package, record.Test)
		switch record.Action {
		case fragmentActionStart:
			if record.Test != fragmentEmptyValue {
				return fmt.Errorf(fragmentLogOpenFormat, path)
			}
			packages[record.Package] = false
		case fragmentActionRun:
			if _, started := packages[record.Package]; !started || record.Test == fragmentEmptyValue ||
				packages[record.Package] {
				return fmt.Errorf(fragmentLogOpenFormat, path)
			}
			tests[key]++
		case fragmentActionPass, fragmentActionSkip:
			if record.Test == fragmentEmptyValue {
				if _, started := packages[record.Package]; !started || packages[record.Package] {
					return fmt.Errorf(fragmentLogOpenFormat, path)
				}
				packages[record.Package] = true
			} else {
				if tests[key] == fragmentEmptyCount {
					return fmt.Errorf(fragmentLogOpenFormat, path)
				}
				tests[key]--
			}
			if record.Action == fragmentActionPass {
				passed++
			}
		case fragmentActionFail:
			return fmt.Errorf(fragmentLogFailedFormat, path)
		case fragmentActionOutput, fragmentActionPause, fragmentActionContinue, fragmentActionBench:
		default:
			return fmt.Errorf(fragmentActionError, path, record.Action)
		}
	}
	for _, complete := range packages {
		if !complete {
			return fmt.Errorf(fragmentLogOpenFormat, path)
		}
	}
	for _, count := range tests {
		if count != fragmentEmptyCount {
			return fmt.Errorf(fragmentLogOpenFormat, path)
		}
	}
	if passed == fragmentEmptyCount {
		return fmt.Errorf(fragmentLogNoPassFormat, path)
	}
	return nil
}

func fragmentExpectedPackage(path string) string {
	suite := strings.TrimSuffix(filepath.Base(path), fragmentLogSuffix)
	suite = strings.TrimSuffix(suite, raceSuffix)
	switch suite {
	case fragmentIntegrationLane:
		return fragmentPackagePrefix + fragmentIntegrationPath
	case fragmentToolsSuite:
		return fragmentPackagePrefix + fragmentToolsSuite
	case fragmentDispatSuite:
		return fragmentPackagePrefix + fragmentDispatPath
	default:
		return fragmentPackagePrefix + fragmentLibraryPath + suite
	}
}

func writeFragment(out string, manifest fragmentManifest, files map[string][]byte) (err error) {
	if err := os.Mkdir(out, fragmentDirectoryMode); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(out))
		}
	}()
	coverage := filepath.Join(out, fragmentCoverageName)
	if err := writeFragmentFiles(coverage, files); err != nil {
		return err
	}
	body, err := json.MarshalIndent(manifest, fragmentEmptyValue, fragmentManifestIndent)
	if err != nil {
		return err
	}
	body = append(body, fragmentManifestNewline)
	return os.WriteFile(filepath.Join(out, fragmentManifestName), body, fragmentFileMode)
}

func writeFragmentFiles(root string, files map[string][]byte) error {
	if err := os.Mkdir(root, fragmentDirectoryMode); err != nil {
		return err
	}
	paths := make([]string, fragmentEmptyCount, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		target := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(target), fragmentDirectoryMode); err != nil {
			return err
		}
		if err := os.WriteFile(target, files[path], fragmentFileMode); err != nil {
			return err
		}
	}
	return nil
}

func mergeFragments(options fragmentOptions) (err error) {
	inputInfo, err := os.Lstat(options.Input)
	if err != nil {
		return err
	}
	if !inputInfo.IsDir() {
		return fmt.Errorf(fragmentUnexpectedFormat, options.Input, fragmentInputDirectory)
	}
	children, err := os.ReadDir(options.Input)
	if err != nil {
		return err
	}
	if len(children) != len(fragmentLanes) {
		return fmt.Errorf(fragmentChildCountError, len(children), len(fragmentLanes))
	}
	merged := make(map[string][]byte)
	for _, lane := range fragmentLanes {
		child := filepath.Join(options.Input, fragmentArtifactPrefix+lane)
		files, err := readCollectedFragment(fragmentReadOptions{
			Root: child, Lane: lane, Commit: options.Commit, Full: options.Full,
		})
		if err != nil {
			return err
		}
		for path, data := range files {
			if _, duplicate := merged[path]; duplicate {
				return fmt.Errorf(fragmentDuplicateError, path)
			}
			merged[path] = data
		}
	}
	for _, child := range children {
		if !slices.Contains(fragmentLanes, strings.TrimPrefix(child.Name(), fragmentArtifactPrefix)) ||
			!strings.HasPrefix(child.Name(), fragmentArtifactPrefix) || !child.IsDir() {
			return fmt.Errorf(fragmentUnexpectedFormat, child.Name(), options.Input)
		}
	}
	if err := os.Mkdir(options.Out, fragmentDirectoryMode); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(options.Out))
		}
	}()
	return writeMergedFiles(options.Out, merged)
}

func writeMergedFiles(out string, files map[string][]byte) error {
	paths := make([]string, fragmentEmptyCount, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		target := filepath.Join(out, path)
		if err := os.MkdirAll(filepath.Dir(target), fragmentDirectoryMode); err != nil {
			return err
		}
		if err := os.WriteFile(target, files[path], fragmentFileMode); err != nil {
			return err
		}
	}
	return nil
}

func readCollectedFragment(options fragmentReadOptions) (map[string][]byte, error) {
	info, err := os.Lstat(options.Root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf(fragmentUnexpectedFormat, options.Root, fragmentInputLayout)
	}
	entries, err := os.ReadDir(options.Root)
	if err != nil {
		return nil, err
	}
	hasCoverage := len(entries) == fragmentWithCoverageCount &&
		entries[fragmentEmptyCount].Name() == fragmentCoverageName && entries[fragmentEmptyCount].IsDir()
	manifestEntry := len(entries) == fragmentManifestOnlyCount &&
		entries[fragmentEmptyCount].Name() == fragmentManifestName &&
		entries[0].Type().IsRegular() || hasCoverage && entries[1].Name() == fragmentManifestName &&
		entries[1].Type().IsRegular()
	if !manifestEntry {
		return nil, fmt.Errorf(fragmentUnexpectedFormat, options.Root, fragmentLayout)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(options.Root, fragmentManifestName))
	if err != nil {
		return nil, err
	}
	var manifest fragmentManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf(fragmentManifestError, options.Root, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf(fragmentManifestTrailing, options.Root)
	}
	if manifest.SchemaVersion != fragmentSchemaVersion || manifest.Source != options.Commit ||
		manifest.Lane != options.Lane || manifest.Full != options.Full {
		return nil, fmt.Errorf(fragmentManifestMismatch, options.Root)
	}
	if !hasCoverage && len(manifest.Files) != fragmentEmptyCount {
		return nil, fmt.Errorf(fragmentMissingFormat, fragmentCoverageName, options.Root)
	}
	files, err := readFragmentFiles(fragmentReadOptions{
		Root: filepath.Join(options.Root, fragmentCoverageName), Lane: options.Lane,
		Commit: options.Commit, Full: options.Full,
	})
	if err != nil {
		return nil, err
	}
	actual := newFragmentManifest(fragmentOptions{
		Commit: options.Commit, Lane: options.Lane, Full: options.Full,
	}, files)
	if !slices.Equal(manifest.Files, actual.Files) {
		return nil, fmt.Errorf(fragmentDigestFormat, options.Root)
	}
	return files, nil
}
