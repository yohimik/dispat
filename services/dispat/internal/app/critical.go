package app

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// A critical is a failure that happens after the point of no return: a
// package's publish script succeeded, or the release commit exists. From that
// moment a run has nothing left to decide — the artefacts are out — and only
// things left to record: the remaining tags, the push, the release records.
//
// So a critical never stops anything. It is logged with its diagnostic code,
// collected, and the run continues to the end of everything it owed. What the
// collection buys is the exit status: the command still fails, once, at the
// end, because a release whose tag was lost must not look like a clean run to
// whoever reads the exit code.
//
// The alternative — returning the error where it happens — is what this
// replaces. It reported a published package as failed, reverted the folder of
// a package already on its registry, cascade-skipped consumers that had
// nothing wrong with them, and left the rest of the run's durable records
// unwritten because one of them failed.

// criticals collects them. The zero value is ready; a nil *criticals accepts
// records and drops them, which is what the paths with no run to report to
// use.
type criticals struct {
	mu   sync.Mutex
	errs []error
}

// record logs one critical and keeps it. code is the diagnostic code, msg the
// human sentence, and with adds the fields the event should carry.
func (c *criticals) record(log zerolog.Logger, code string, err error, msg string, with func(*zerolog.Event) *zerolog.Event) {
	ev := log.Error().Err(err).Str("code", code)
	if with != nil {
		ev = with(ev)
	}
	ev.Msg(msg)
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errs = append(c.errs, fmt.Errorf("%s: %s: %w", code, msg, err))
}

// adopt takes in the criticals the executor recorded on each package's result.
// They are kept there because they are that package's news — the summary line
// says which release is missing part of its record — and gathered here because
// the exit status is the run's.
func (c *criticals) adopt(results map[string]*release.Result) {
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names) // one run, one order
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, name := range names {
		for _, err := range results[name].Critical {
			c.errs = append(c.errs, fmt.Errorf("%s: %w", name, err))
		}
	}
}

// len reports how many were collected.
func (c *criticals) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.errs)
}

// err joins everything collected into the one error the command returns, or
// nil when the run had none. It is the last thing a command does, so the work
// is finished by the time anyone sees it.
func (c *criticals) err() error {
	if c.len() == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Errorf("%d step(s) failed after their release was already out: %w",
		len(c.errs), errors.Join(c.errs...))
}
