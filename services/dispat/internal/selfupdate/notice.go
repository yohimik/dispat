package selfupdate

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/yohimik/dispat/pkg/ccme"
)

// CheckTimeout bounds the background check. It is short on purpose: the
// answer is worth having, and worth nothing at all if waiting for it is
// something anybody notices.
const CheckTimeout = 2 * time.Second

// Result is what a check learned. It only ever exists when the check
// succeeded, so a caller holding one can read it without asking whether it
// worked.
type Result struct {
	Current ccme.Version
	Latest  ccme.Version
	Tag     string
	Origin  Origin
}

// Behind reports whether the release found is newer than what is running.
func (r Result) Behind() bool { return r.Latest.Compare(r.Current) > 0 }

// Check asks, in the background, whether a newer stable release is out.
//
// The channel carries at most one result and is never closed: a check that
// fails, times out, or simply loses the race against the command sends
// nothing, and the caller's non-blocking receive falls through to its default.
// That collapses offline, rate-limited, slow and unfinished into one
// behaviour, which is silence, and it is why nothing here reports an error.
//
// The caller owns ctx and cancels it on the way out, which tears the request
// down rather than leaving it running.
func Check(ctx context.Context, s Source, build Build) <-chan Result {
	out := make(chan Result, 1)
	current, err := ccme.ParseVersion(build.Version)
	if err != nil {
		// A local build compares to nothing.
		return out
	}
	if s.Client == nil {
		s.Client = &http.Client{Timeout: CheckTimeout}
	}
	// Stable releases only, whatever the command asks for: someone running a
	// release candidate wants to hear about the stable it leads to, not about
	// the next candidate.
	s.Prerelease = false
	go func() {
		rel, err := s.Latest(ctx)
		if err != nil {
			s.Log.Debug().Err(err).Msg("selfupdate: the update check found nothing")
			return
		}
		out <- Result{Current: current, Latest: rel.Version, Tag: rel.Tag, Origin: build.Origin}
	}()
	return out
}

// Notice is what a command prints on its way out when the check came back:
// nothing at all when the binary is current, and otherwise the version that
// is out, how to install it, and on macOS what installing it will take.
//
// A `go install` build is told the version and nothing else. Suggesting
// `dispat self-update` there would be wrong, since the next `go install`
// would undo it, and the command that knows the right answer is the one that
// prints it.
func Notice(res Result, goos string) string {
	if !res.Behind() {
		return ""
	}
	var b strings.Builder
	b.WriteString("\na newer stable release is available: " + res.Latest.String() +
		" (you have " + res.Current.String() + ")\n")
	if res.Origin == OriginRelease {
		b.WriteString("run \"dispat self-update\" to install it\n")
		if note := MacNote(goos, ""); note != "" {
			b.WriteString(note)
		}
	}
	return b.String()
}

// Status is the line --version adds when the check answered in time: the
// notice, or a plain statement when there is nothing to install.
func Status(res Result, goos string) string {
	if notice := Notice(res, goos); notice != "" {
		return notice
	}
	return "\nthis is the latest stable release\n"
}

// MacNote is the warning that dispat's binaries are not notarised. macOS can
// refuse to open one, and being told after an update that the tool no longer
// starts is the wrong moment to find out, so it is said before an update is
// suggested and again after one is installed. path is the installed binary
// when there is one to name.
func MacNote(goos, path string) string {
	if goos != "darwin" {
		return ""
	}
	note := "\nnote: dispat's macOS binaries are not notarised, so the first run after an update\n" +
		"may need allowing under System Settings > Privacy & Security.\n"
	if path != "" {
		note += "If macOS refuses to open it: xattr -d com.apple.quarantine " + path + "\n"
	}
	return note
}
