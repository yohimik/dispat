package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
	"gopkg.in/yaml.v3"
)

func TestRewriteAquaPreservesLayoutQuotesCommentsAndCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aqua.yaml")
	in := "# prefix\r\npackages:\r\n  - name: 'cli/cli@v2.0.0' # inline\r\n  - name: tool/name\r\n    registry: private\r\n    version: \"1.0.0\" # separate\r\n"
	if err := os.WriteFile(path, []byte(in), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := Rewrite(path, "9.9.9", []Edit{{Name: "cli/cli", Range: "v2.1.0"}, {Name: "private:tool/name", Range: "1.1.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 2 || res.VersionWritten {
		t.Fatalf("unexpected result: %#v", res)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	want := strings.Replace(in, "cli/cli@v2.0.0", "cli/cli@v2.1.0", 1)
	want = strings.Replace(want, "1.0.0", "1.1.0", 1)
	if got != want {
		t.Fatalf("write changed surrounding bytes\ngot %q\nwant %q", got, want)
	}
}

func FuzzRewriteAquaNeverCommitsInvalidYAML(f *testing.F) {
	f.Add([]byte("packages:\n- name: cli/cli@v1.0.0\n"))
	f.Add([]byte("packages: [{name: cli/cli@v1.0.0}]\n"))
	f.Add([]byte("version: &v v1\npackages:\n- name: cli/cli\n  version: *v\n"))
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 64<<10 {
			return
		}
		path := filepath.Join(t.TempDir(), "aqua.yaml")
		if err := os.WriteFile(path, src, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Rewrite(path, "", []Edit{{Name: "cli/cli", Range: "v2.0.0"}})
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err != nil {
			if string(got) != string(src) {
				t.Fatal("failed rewrite changed the file")
			}
			return
		}
		var doc yaml.Node
		if yaml.Unmarshal(got, &doc) != nil {
			t.Fatal("successful rewrite committed invalid YAML")
		}
	})
}

func TestRewriteAsAquaImportedFilenameAndDynamicSkip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.inc")
	if err := os.WriteFile(path, []byte("packages:\n- name: cli/cli\n  version_expr: env.VERSION\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := RewriteAs(path, manifest.FormatAqua, "", []Edit{{Name: "cli/cli", Range: "v2.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("unexpected result: %#v", res)
	}
}

func TestRewriteAquaRefusesUnsafeYAMLWithoutWriting(t *testing.T) {
	cases := map[string]string{
		"flow":             "packages: [{name: cli/cli@v1.0.0}]\n",
		"alias":            "tool: &tool\n  name: cli/cli@v1.0.0\npackages:\n- *tool\n",
		"version alias":    "shared: &version v1.0.0\npackages:\n- name: cli/cli\n  version: *version\n",
		"packages alias":   "shared: &packages\n- name: cli/cli@v1.0.0\npackages: *packages\n",
		"anchored name":    "packages:\n- name: &tool cli/cli@v1.0.0\n",
		"block":            "packages:\n- name: cli/cli\n  version: |\n    v1.0.0\n",
		"multiline quoted": "packages:\n- name: cli/cli\n  version: \"v1.0.0\\\n    suffix\"\n",
		"nonmapping":       "- packages\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "aqua.yaml")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Rewrite(path, "", []Edit{{Name: "cli/cli", Range: "v2.0.0"}}); err == nil {
				t.Fatal("expected safe refusal")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != src {
				t.Fatalf("refusal changed file: %q", got)
			}
		})
	}
}
