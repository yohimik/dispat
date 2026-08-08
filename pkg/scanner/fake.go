package scanner

import "context"

// Fake is an in-memory Scanner for tests: manifests keyed by the scanned
// folder, returned verbatim alongside the configured error.
type Fake struct {
	// Manifests holds each folder's scan result, keyed by the dir argument.
	Manifests map[string][]Manifest
	// Err is joined onto every Scan result, mimicking a partial scan.
	Err error
}

// Scan implements Scanner.
func (f Fake) Scan(_ context.Context, dir string) ([]Manifest, error) {
	return f.Manifests[dir], f.Err
}
