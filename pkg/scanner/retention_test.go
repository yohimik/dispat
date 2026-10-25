package scanner

import (
	"reflect"
	"testing"
)

// A manifest is held for the whole run, so what it keeps alive must be its
// own few values rather than the file they were read from. The file here is
// four megabytes, the size of a large project.pbxproj; anything near that much
// still live after a collection means a value still points into the text.
func TestLineScannedParsersRetainOnlyTheirValues(t *testing.T) {
	const (
		size        = 4 << 20
		allowedLive = 64 << 10
	)
	for _, c := range lineScannedCases {
		t.Run(c.name, func(t *testing.T) {
			want, err := c.parse(c.rel, []byte(c.head))
			if err != nil {
				t.Fatal(err)
			}
			got, retained, err := c.parseRetaining(size)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("the long file parsed as %+v, its head alone as %+v", got, want)
			}
			if retained > allowedLive {
				t.Errorf("the manifest keeps %d bytes of a %d-byte file alive, want at most %d", retained, size, allowedLive)
			}
		})
	}
}
