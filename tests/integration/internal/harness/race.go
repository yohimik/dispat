package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CheckRaceReports makes a race in a black-box child fail the suite even when
// the scenario intentionally accepts a non-zero command exit. Go's race
// runtime writes one race.<pid> file per reporting process under dir.
func CheckRaceReports(dir string) error {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading subprocess race reports: %w", err)
	}
	var reports []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "race.") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading subprocess race report %s: %w", path, readErr)
		}
		reports = append(reports, fmt.Sprintf("%s:\n%s", path, body))
	}
	if len(reports) == 0 {
		return nil
	}
	sort.Strings(reports)
	return fmt.Errorf("race detector reported in a black-box subprocess:\n%s", strings.Join(reports, "\n"))
}
