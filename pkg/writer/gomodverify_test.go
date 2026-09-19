package writer

import (
	"strings"
	"testing"
)

// The module graph rejects a major version a module path does not carry:
// requiring example.com/core v2.0.0 when the path does not end in /v2 gives a
// go.mod the toolchain refuses to load. x/mod's formatter writes it happily,
// so the writer has to re-read its own output, the way every other writer here
// proves its result still parses.

func TestGoModRewriteRefusesARequirementTheToolchainWouldReject(t *testing.T) {
	src := "module example.com/app\n\ngo 1.26\n\nrequire example.com/core v1.0.0\n"
	path := seed(t, "go.mod", src)
	res, err := Rewrite(path, "", []Edit{{Name: "example.com/core", Range: "v2.0.0"}})
	if err == nil {
		t.Fatalf("a major bump without a path change was accepted: %+v", res)
	}
	if !strings.Contains(err.Error(), "unparseable go.mod") {
		t.Errorf("error = %v", err)
	}
	if got := read(t, path); got != src {
		t.Errorf("a refused rewrite changed the file:\n%s", got)
	}
}

func TestGoModRewriteAcceptsAMajorBumpThatCarriesItsPath(t *testing.T) {
	src := "module example.com/app\n\ngo 1.26\n\nrequire example.com/core/v2 v2.0.0\n"
	path := seed(t, "go.mod", src)
	res, err := Rewrite(path, "", []Edit{{Name: "example.com/core/v2", Range: "v2.1.0"}})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if len(res.Applied) != 1 || !strings.Contains(read(t, path), "v2.1.0") {
		t.Fatalf("rewrite = %+v\n%s", res, read(t, path))
	}
}
