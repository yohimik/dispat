package writer

import (
	"os"
	"strings"
	"testing"
)

func TestPlistRewritePreservesEveryOtherByte(t *testing.T) {
	// Deliberately odd formatting: tabs, a nested dictionary carrying the same
	// keys, a non-string value, no trailing newline. Only the root
	// dictionary's marketing version may change.
	src := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleShortVersionString</key>
			<string>9.9.9</string>
		</dict>
	</array>
	<key>CFBundleIdentifier</key>
	<string>com.acme.app</string>
	<key>CFBundleShortVersionString</key>	<string>1.0.0</string>
	<key>CFBundleVersion</key>
	<string>42</string>
	<key>LSRequiresIPhoneOS</key>
	<true/>
</dict>
</plist>`
	path := seed(t, "Info.plist", src)

	res, err := Rewrite(path, "2.0.0", []Edit{{Name: "ghost", Range: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(src,
		"<key>CFBundleShortVersionString</key>\t<string>1.0.0</string>",
		"<key>CFBundleShortVersionString</key>\t<string>2.0.0</string>", 1)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !res.VersionWritten || len(res.Applied) != 0 {
		t.Errorf("result mismatch: %+v", res)
	}
	// A plist declares no dependencies, so an edit is missing by definition.
	if len(res.Missing) != 1 || res.Missing[0].Name != "ghost" {
		t.Errorf("missing mismatch: %+v", res.Missing)
	}
}

func TestPlistRewriteRefusesBuildSettingReference(t *testing.T) {
	// Replacing $(MARKETING_VERSION) with a literal silently severs the
	// project's build-setting indirection: the version stops tracking the
	// Xcode setting and every future build ships whatever was frozen here.
	src := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleShortVersionString</key>
	<string>$(MARKETING_VERSION)</string>
</dict>
</plist>
`
	path := seed(t, "Info.plist", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten {
		t.Error("a build-setting reference must not be overwritten")
	}
	if got := read(t, path); got != src {
		t.Errorf("file was modified:\n got: %q\nwant: %q", got, src)
	}
}

func TestPlistRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "<plist version=\"1.0\">\n<dict>\n\t<key>CFBundleShortVersionString</key>\n\t<string>1.0.0</string>\n</dict>\n</plist>\n"
	path := seed(t, "Info.plist", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Rewrite(path, "1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != src || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("no-op rewrite touched the file: %+v", res)
	}
}

func TestPlistRewriteWithoutVersionKeyLeavesFileAlone(t *testing.T) {
	src := "<plist version=\"1.0\">\n<dict>\n\t<key>CFBundleVersion</key>\n\t<string>42</string>\n</dict>\n</plist>\n"
	path := seed(t, "Info.plist", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != src {
		t.Errorf("an absent marketing version has nothing to write: %+v", res)
	}
}

func TestPlistRewriteEscapesAndSkipsSelfClosingValue(t *testing.T) {
	// A self-closing <string/> has no content bytes to splice into; writing
	// after the tag would land the version outside the element.
	src := "<plist version=\"1.0\">\n<dict>\n\t<key>CFBundleShortVersionString</key>\n\t<string/>\n</dict>\n</plist>\n"
	path := seed(t, "Info.plist", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != src {
		t.Errorf("a self-closing value must be declined: %+v", res)
	}

	// A version carrying XML metacharacters is escaped, never spliced raw.
	src = "<plist version=\"1.0\">\n<dict>\n\t<key>CFBundleShortVersionString</key>\n\t<string>1.0.0</string>\n</dict>\n</plist>\n"
	path = seed(t, "Info.plist", src)
	if _, err := Rewrite(path, "1.0.0+a<b&c", nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.Contains(got, "<string>1.0.0+a&lt;b&amp;c</string>") {
		t.Errorf("metacharacters not escaped: %q", got)
	}
}

func TestPlistRewriteRefusesLegacyEncoding(t *testing.T) {
	// Transcoding would shift every byte offset the splice depends on, so a
	// non-UTF-8 declaration is refused rather than rewritten wrongly.
	src := `<?xml version="1.0" encoding="ISO-8859-1"?>
<plist version="1.0">
<dict>
	<key>CFBundleShortVersionString</key>
	<string>1.0.0</string>
</dict>
</plist>
`
	path := seed(t, "Info.plist", src)
	if _, err := Rewrite(path, "2.0.0", nil); err == nil {
		t.Error("a legacy-encoded plist must be refused")
	}
	if read(t, path) != src {
		t.Error("a failed rewrite modified the file")
	}
}
