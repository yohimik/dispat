package writer

import (
	"strings"
	"testing"
)

// An Xcode build setting that defers to another setting is a reference, not a
// version: the value lives in an xcconfig or in a parent configuration, and a
// literal written over it would freeze the number the reference exists to
// track. The package contract says such a value is left alone, and the guards
// that stand in for re-parsing a project file must not count it either, or a
// project carrying one could not be version-written at all.

func TestXcodeProjRewriteLeavesADeferredMarketingVersionAlone(t *testing.T) {
	src := "{\n\tobjects = {\n" +
		"\t\tDEBUG = {\n\t\t\tbuildSettings = {\n\t\t\t\tMARKETING_VERSION = 1.0.0;\n\t\t\t};\n\t\t};\n" +
		"\t\tRELEASE = {\n\t\t\tbuildSettings = {\n\t\t\t\tMARKETING_VERSION = \"$(MARKETING_VERSION)\";\n\t\t\t};\n\t\t};\n" +
		"\t\tSHARED = {\n\t\t\tbuildSettings = {\n\t\t\t\tMARKETING_VERSION = \"${ACME_VERSION}\";\n\t\t\t};\n\t\t};\n" +
		"\t};\n}\n"
	path := seed(t, "project.pbxproj", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !res.VersionWritten {
		t.Fatalf("the literal configuration was not written: %+v", res)
	}
	got := read(t, path)
	if !strings.Contains(got, "MARKETING_VERSION = 2.0.0;") {
		t.Errorf("the literal setting was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, `MARKETING_VERSION = "$(MARKETING_VERSION)";`) ||
		!strings.Contains(got, `MARKETING_VERSION = "${ACME_VERSION}";`) {
		t.Errorf("a deferred setting was overwritten:\n%s", got)
	}
}

func TestXcodeProjRewriteWithOnlyDeferredVersionsWritesNothing(t *testing.T) {
	src := "{\n\tbuildSettings = {\n\t\tMARKETING_VERSION = \"$(MARKETING_VERSION)\";\n\t};\n}\n"
	path := seed(t, "project.pbxproj", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.VersionWritten || read(t, path) != src {
		t.Errorf("a wholly deferred project was rewritten: %+v", res)
	}
}

func TestXcodeBuildWriteLeavesADeferredCounterAlone(t *testing.T) {
	src := "{\n\tobjects = {\n" +
		"\t\tDEBUG = {\n\t\t\tbuildSettings = {\n\t\t\t\tCURRENT_PROJECT_VERSION = 7;\n\t\t\t};\n\t\t};\n" +
		"\t\tRELEASE = {\n\t\t\tbuildSettings = {\n\t\t\t\tCURRENT_PROJECT_VERSION = \"$(CURRENT_PROJECT_VERSION)\";\n\t\t\t};\n\t\t};\n" +
		"\t};\n}\n"
	path := seed(t, "project.pbxproj", src)
	res, err := SetBuild(path, "42")
	if err != nil {
		t.Fatalf("set build: %v", err)
	}
	if !res.BuildWritten {
		t.Fatalf("the literal configuration was not written: %+v", res)
	}
	got := read(t, path)
	if !strings.Contains(got, "CURRENT_PROJECT_VERSION = 42;") {
		t.Errorf("the literal counter was not written:\n%s", got)
	}
	if !strings.Contains(got, `CURRENT_PROJECT_VERSION = "$(CURRENT_PROJECT_VERSION)";`) {
		t.Errorf("a deferred counter was overwritten:\n%s", got)
	}
}
