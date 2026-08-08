package harness

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Interval is one label's [Start, End) execution window, as recorded by the
// tsmark probe (see cmd/tsmark for why real timestamps are recorded instead
// of reading dispat's own logs).
type Interval struct {
	Label      string
	Start, End time.Time
}

// ParseTimeline reads a tsmark log file and pairs each label's start/end
// lines into an Interval, sorted by start time. A label that started but
// never ended fails the test immediately — the run crashed, or the script
// never ran to completion — rather than silently dropping it from the
// intervals a test is about to reason over.
func ParseTimeline(t testing.TB, path string) []Interval {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading timeline log %s", path)

	starts := map[string]time.Time{}
	ends := map[string]time.Time{}
	var order []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		require.Lenf(t, fields, 3, "malformed timeline line %q in %s", line, path)
		label, event, stamp := fields[0], fields[1], fields[2]
		n, err := strconv.ParseInt(stamp, 10, 64)
		require.NoErrorf(t, err, "malformed timestamp in %q", line)
		at := time.Unix(0, n)
		switch event {
		case "start":
			// A label recorded twice (a scenario running two releases into one
			// log, say) would silently keep only the last pair and let a wrong
			// assertion pass green; per-run logs must use distinct labels.
			_, dup := starts[label]
			require.Falsef(t, dup, "label %q recorded twice in %s: use distinct labels per run", label, path)
			starts[label] = at
			order = append(order, label)
		case "end":
			_, dup := ends[label]
			require.Falsef(t, dup, "label %q ended twice in %s: use distinct labels per run", label, path)
			ends[label] = at
		default:
			t.Fatalf("unknown timeline event %q in %q", event, line)
		}
	}

	out := make([]Interval, 0, len(starts))
	for _, label := range order {
		end, ok := ends[label]
		require.Truef(t, ok, "label %q started but never ended in %s", label, path)
		out = append(out, Interval{Label: label, Start: starts[label], End: end})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Find returns the interval with the given label, failing the test if it is
// not present.
func Find(t testing.TB, ivs []Interval, label string) Interval {
	t.Helper()
	for _, iv := range ivs {
		if iv.Label == label {
			return iv
		}
	}
	t.Fatalf("no interval labelled %q among %d recorded", label, len(ivs))
	return Interval{}
}

// sweepMaxOverlap returns the largest number of intervals simultaneously in
// flight, via a sweep over start/end events. Two touching endpoints — one
// interval ending exactly when another starts — do not count as overlapping.
func sweepMaxOverlap(ivs []Interval) int {
	type event struct {
		at    time.Time
		delta int
	}
	events := make([]event, 0, len(ivs)*2)
	for _, iv := range ivs {
		events = append(events, event{iv.Start, +1}, event{iv.End, -1})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at.Equal(events[j].at) {
			return events[i].delta < events[j].delta // ends sort before starts at a tie
		}
		return events[i].at.Before(events[j].at)
	})
	cur, peak := 0, 0
	for _, e := range events {
		cur += e.delta
		if cur > peak {
			peak = cur
		}
	}
	return peak
}

// bruteMaxOverlap recomputes the same quantity by pairwise counting — O(n²),
// fine for the handful of intervals a test records. It is an independently
// written cross-check: the two implementations must agree before either is
// trusted, so a tie-breaking bug in the sweep cannot quietly agree a real
// scheduling defect out of existence.
func bruteMaxOverlap(ivs []Interval) int {
	peak := 0
	for _, a := range ivs {
		n := 0
		for _, b := range ivs {
			if a.Start.Before(b.End) && b.Start.Before(a.End) {
				n++
			}
		}
		if n > peak {
			peak = n
		}
	}
	return peak
}

// AssertConcurrencyBudget asserts the strongest claim a timeline supports
// about a stage budget: the peak number of intervals in flight was *exactly*
// min(budget, len(ivs)). "At most budget" alone would be satisfied by a
// scheduler that serialised everything, and "budget reached" alone by one
// that ignored the limit — only the exact peak proves both halves. The peak
// is computed twice by independent implementations that must agree, and the
// budget-plus-first rule is then re-derived a third way from start order
// alone.
func AssertConcurrencyBudget(t testing.TB, ivs []Interval, budget int) {
	t.Helper()
	sweep := sweepMaxOverlap(ivs)
	brute := bruteMaxOverlap(ivs)
	require.Equalf(t, brute, sweep,
		"independently computed overlap counts disagree (%d vs %d) — a harness bug, not a scheduler one", brute, sweep)
	want := min(budget, len(ivs))
	assert.Equalf(t, want, sweep,
		"peak concurrency was %d, want exactly %d (budget %d over %d tasks)", sweep, want, budget, len(ivs))
	assertNthWaitsForSlot(t, ivs, budget)
}

// assertNthWaitsForSlot re-derives the budget guarantee by pure start-order
// reasoning instead of counting: sorted by start time, the (budget+1)-th
// interval to start must not begin before at least one of the first `budget`
// has ended — otherwise more than `budget` were genuinely in flight the
// instant it began, whatever any overlap count says.
func assertNthWaitsForSlot(t testing.TB, ivs []Interval, budget int) {
	t.Helper()
	if len(ivs) <= budget || budget <= 0 {
		return
	}
	sorted := append([]Interval(nil), ivs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	nth := sorted[budget] // 0-indexed: the (budget+1)-th to start
	freedSlot := false
	for _, iv := range sorted[:budget] {
		if !nth.Start.Before(iv.End) {
			freedSlot = true
			break
		}
	}
	assert.Truef(t, freedSlot, "%q started at %s before any of the first %d tasks finished",
		nth.Label, nth.Start.Format(time.RFC3339Nano), budget)
}

// AssertSequential asserts that after did not start before before ended —
// the ordering a dependency edge (or a gating flag like
// isBuildWaitingPublish) must impose between a provider's stage and a
// consumer's. The assertion is structural, not statistical: it holds
// whatever the scripts' durations, so it can never flake.
func AssertSequential(t testing.TB, before, after Interval) {
	t.Helper()
	assert.Falsef(t, after.Start.Before(before.End),
		"%q (ends %s) must finish before %q starts (%s), but it did not",
		before.Label, before.End.Format(time.RFC3339Nano), after.Label, after.Start.Format(time.RFC3339Nano))
}

// AssertOverlaps asserts that a and b were in flight at the same instant —
// evidence that independent work was actually picked up concurrently, not
// merely left unordered. Unlike AssertSequential this is a timing claim, so
// callers give the underlying scripts sleeps long enough to dwarf process
// launch jitter.
func AssertOverlaps(t testing.TB, a, b Interval) {
	t.Helper()
	assert.Truef(t, a.Start.Before(b.End) && b.Start.Before(a.End),
		"%q [%s .. %s] and %q [%s .. %s] were expected to overlap but did not",
		a.Label, a.Start.Format(time.RFC3339Nano), a.End.Format(time.RFC3339Nano),
		b.Label, b.Start.Format(time.RFC3339Nano), b.End.Format(time.RFC3339Nano))
}
