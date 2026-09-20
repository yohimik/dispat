package harness

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileProxyURLUsesGoProxyFileSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, path, goos, want string
	}{
		{"unix path unchanged", "/tmp/dispat proxy", "linux", "file:///tmp/dispat%20proxy"},
		{"windows drive is an absolute path", `C:\Users\ci\proxy`, "windows", "file:///C:/Users/ci/proxy"},
		{"windows UNC server is the host", `\\server\share\proxy`, "windows", "file://server/share/proxy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatFileProxyURL(tc.path, tc.goos); got != tc.want {
				t.Fatalf("formatFileProxyURL(%q, %q) = %q, want %q", tc.path, tc.goos, got, tc.want)
			}
		})
	}
}

func TestExternalModuleRequirementsKeepsWorkspaceSelectionsOnly(t *testing.T) {
	list := `github.com/yohimik/dispat/services/dispat
golang.org/x/text v0.28.0
github.com/yohimik/dispat/pkg/models
golang.org/x/text v0.29.0
github.com/rs/zerolog v1.35.1
example.com/versionless
`
	assert.Equal(t, []string{
		"github.com/rs/zerolog@v1.35.1",
		"golang.org/x/text@v0.29.0",
	}, collectExternalModuleRequirements(list))
}
