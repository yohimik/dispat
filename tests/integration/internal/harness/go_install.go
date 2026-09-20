package harness

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const dispatModule = "github.com/yohimik/dispat/services/dispat"

// BuildGoInstalled installs the current dispat module source through a local
// module proxy. Unlike go build, go install module@version records version in
// runtime build info, which is the supported origin signal self-update reads.
func BuildGoInstalled(t testing.TB, version string) string {
	t.Helper()
	if compiler() != "go" {
		t.Skip("a go install origin requires the Go compiler")
	}
	if prebuiltBin() != "" {
		t.Skip("a prebuilt integration run cannot create a source go install fixture")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("finding Go toolchain: %v", err)
	}
	root, err := monorepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	moduleDir := filepath.Join(root, "services", "dispat")
	proxy := t.TempDir()
	moduleVersion := "v" + strings.TrimPrefix(version, "v")
	externalRequirements := readWorkspaceExternalModuleRequirements(t, goBin, root)
	mod, err := os.ReadFile(filepath.Join(moduleDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	// CI links first-party modules before running the suite. Published module
	// archives cannot contain those machine-local replacements.
	mod = unlinkProxyModule(t, goBin, mod)
	mod = editModuleRequirements(t, goBin, mod, externalRequirements)
	for _, name := range []string{"ccme", "config", "manifest", "models", "scanner", "writer"} {
		modulePath := "github.com/yohimik/dispat/pkg/" + name
		const localVersion = "v1.99.0"
		lines := strings.Split(string(mod), "\n")
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), modulePath+" ") {
				lines[i] = "\t" + modulePath + " " + localVersion
			}
		}
		mod = []byte(strings.Join(lines, "\n"))
		packageDir := filepath.Join(root, "pkg", name)
		packageMod, readErr := os.ReadFile(filepath.Join(packageDir, "go.mod"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		packageMod = unlinkProxyModule(t, goBin, packageMod)
		writeProxyModule(t, proxy, modulePath, localVersion, packageDir, packageMod)
	}
	writeProxyModule(t, proxy, dispatModule, moduleVersion, moduleDir, mod)

	binDir := t.TempDir()
	proxyURL := formatFileProxyURL(proxy, runtime.GOOS)
	cacheCmd := exec.Command(goBin, "env", "GOMODCACHE")
	cacheOut, err := cacheCmd.Output()
	if err != nil {
		t.Fatalf("locating Go module cache: %v", err)
	}
	downloadCacheURL := formatFileProxyURL(filepath.Join(strings.TrimSpace(string(cacheOut)), "cache", "download"), runtime.GOOS)
	args := []string{"install"}
	installDir := ""
	if coverDir() != "" || raceBuild() {
		// Go's install@version path skips PrepareForCoverageBuild. Install
		// the same version-pinned dependency from a disposable caller module
		// instead: Go still records the actual module version in build info,
		// and its ordinary install path instruments the executable.
		installDir = t.TempDir()
		callerMod := "module example.com/dispat-install-fixture\n\ngo 1.26\n\nrequire " + dispatModule + " " + moduleVersion + "\n"
		if err := os.WriteFile(filepath.Join(installDir, "go.mod"), []byte(callerMod), 0o644); err != nil {
			t.Fatal(err)
		}
		if coverDir() != "" {
			args = append(args, "-cover", "-covermode=atomic", "-coverpkg="+productionCoverpkg())
		}
		if raceBuild() {
			args = append(args, "-race")
		}
		args = append(args, dispatModule)
	} else {
		args = append(args, dispatModule+"@"+moduleVersion)
	}
	cmd := exec.Command(goBin, args...)
	cmd.Dir = installDir
	cmd.Env = append(os.Environ(),
		"GOBIN="+binDir,
		"GOMODCACHE="+t.TempDir(),
		"GOFLAGS=-modcacherw -mod=mod",
		"GOWORK=off",
		"GOPROXY="+proxyURL+","+downloadCacheURL+",off",
		"GOSUMDB=off",
		"GONOPROXY=none",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go installing dispat %s: %v\n%s", moduleVersion, err, out)
	}
	return filepath.Join(binDir, "dispat"+goInstallExeSuffix())
}

// readWorkspaceExternalModuleRequirements returns the external module versions
// selected by the workspace that compiled the suite. The Docker dependency
// layer has exactly these versions in its download cache.
func readWorkspaceExternalModuleRequirements(t testing.TB, goBin, root string) []string {
	t.Helper()
	list := exec.Command(goBin, "list", "-deps", "-f", "{{with .Module}}{{.Path}} {{.Version}}{{end}}", dispatModule)
	list.Dir = root
	list.Env = append(os.Environ(), "GOFLAGS=", "GOWORK="+filepath.Join(root, "go.work"), "GOPROXY=off")
	out, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("resolving workspace module versions: %v\n%s", err, out)
	}
	return collectExternalModuleRequirements(string(out))
}

func editModuleRequirements(t testing.TB, goBin string, mod []byte, requirements []string) []byte {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), mod, 0o600); err != nil {
		t.Fatal(err)
	}
	editModuleFileRequirements(t, goBin, dir, requirements)
	out, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func editModuleFileRequirements(t testing.TB, goBin, dir string, requirements []string) {
	t.Helper()
	if len(requirements) == 0 {
		return
	}
	args := []string{"mod", "edit"}
	for _, requirement := range requirements {
		args = append(args, "-require="+requirement)
	}
	edit := exec.Command(goBin, args...)
	edit.Dir = dir
	edit.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOPROXY=off")
	if output, editErr := edit.CombinedOutput(); editErr != nil {
		t.Fatalf("pinning workspace module versions: %v\n%s", editErr, output)
	}
}

func collectExternalModuleRequirements(list string) []string {
	selected := make(map[string]string)
	for _, line := range strings.Split(list, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] == "" || strings.HasPrefix(fields[0], "github.com/yohimik/dispat/") {
			continue
		}
		selected[fields[0]] = fields[1]
	}
	paths := make([]string, 0, len(selected))
	for path := range selected {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	requirements := make([]string, 0, len(paths))
	for _, path := range paths {
		requirements = append(requirements, path+"@"+selected[path])
	}
	return requirements
}

// formatFileProxyURL spells a filesystem folder as the file URL GOPROXY accepts.
// A Windows drive is an absolute URL path, while a UNC server is the URL host.
// Other platforms retain net/url's ordinary native-path serialization.
func formatFileProxyURL(path, goos string) string {
	if goos != "windows" {
		return (&url.URL{Scheme: "file", Path: path}).String()
	}
	normalizedPath := strings.ReplaceAll(path, `\`, "/")
	if strings.HasPrefix(normalizedPath, "//") {
		hostPath := strings.TrimPrefix(normalizedPath, "//")
		host, rest, _ := strings.Cut(hostPath, "/")
		return (&url.URL{Scheme: "file", Host: host, Path: "/" + rest}).String()
	}
	if len(normalizedPath) >= 2 && normalizedPath[1] == ':' {
		normalizedPath = "/" + normalizedPath
	}
	return (&url.URL{Scheme: "file", Path: normalizedPath}).String()
}

func unlinkProxyModule(t testing.TB, goBin string, source []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	modPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(modPath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"mod", "edit"}
	for _, name := range []string{"ccme", "config", "manifest", "models", "scanner", "writer"} {
		args = append(args, "-dropreplace=github.com/yohimik/dispat/pkg/"+name)
	}
	cmd := exec.Command(goBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("preparing proxy module: %v\n%s", err, out)
	}
	mod, err := os.ReadFile(modPath)
	if err != nil {
		t.Fatal(err)
	}
	return mod
}

func writeProxyModule(t testing.TB, proxy, modulePath, version, moduleDir string, mod []byte) {
	t.Helper()
	versionDir := filepath.Join(proxy, filepath.FromSlash(modulePath), "@v")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, version+".mod"), mod, 0o644); err != nil {
		t.Fatal(err)
	}
	info := fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-01-01T00:00:00Z\"}\n", version)
	if err := os.WriteFile(filepath.Join(versionDir, version+".info"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "list"), []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := zipModule(filepath.Join(versionDir, version+".zip"), moduleDir, modulePath, version, mod); err != nil {
		t.Fatalf("packing %s: %v", modulePath, err)
	}
}

func zipModule(destination, moduleDir, modulePath, version string, mod []byte) error {
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	prefix := modulePath + "@" + version + "/"
	walkErr := filepath.WalkDir(moduleDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(moduleDir, path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = prefix + filepath.ToSlash(rel)
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if rel == "go.mod" {
			_, err = writer.Write(mod)
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if walkErr != nil {
		_ = zw.Close()
		_ = out.Close()
		return walkErr
	}
	if err := zw.Close(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func goInstallExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
