package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// The update notice: dispat noticing that a newer dispat is out.
//
// The check runs beside the command rather than in front of it, and the
// answer is printed only if it arrived before the command was done. Nothing
// ever waits for it. A release run takes long enough that the answer is
// always there; a command that finishes first prints nothing, which is the
// right trade, because an update suggestion is not worth a millisecond of
// anyone's time. Offline, rate-limited, slow and unfinished all look the same
// from here, and that sameness is the point: there is one behaviour to reason
// about, and it is silence.

// updateCheckEnv is the environment kill switch, for the commands that read no
// config file and for anyone who wants the check off machine-wide.
const updateCheckEnv = "DISPAT_UPDATE_CHECK"

// notice carries the pending check to the end of the run.
type notice struct {
	ch <-chan selfupdate.Result
	// status makes --version state the answer either way, rather than only
	// when there is something to install.
	status bool
	// wait holds the command back for the answer, bounded by the check's own
	// timeout. Only an environment that explicitly asked for the check gets
	// this: whoever set the variable wants the answer, not a race against it.
	wait bool
}

// startUpdateCheck begins the check, unless this invocation is one that would
// never print the answer: a local build has no version to compare, JSON output
// is read by a machine that cannot act on a suggestion, and both the config
// option and the environment variable are plain refusals.
func startUpdateCheck(ctx context.Context, o *options, fs *pflag.FlagSet, format string, enabled bool) notice {
	build := selfupdate.Describe(Version)
	if build.Origin == selfupdate.OriginDev || format == "json" || !enabled || !envAllowsUpdateCheck() {
		return notice{}
	}
	return notice{ch: selfupdate.Check(ctx, updateSource(o, fs), build), wait: envForcesUpdateCheck()}
}

// print writes the notice when the answer beat the command home — or, when the
// environment explicitly asked for the check, waits for it up to the check's
// own timeout. A failed check sends nothing, so the wait is against the clock
// rather than the channel alone.
func (n notice) print(out io.Writer) {
	if n.ch == nil {
		return
	}
	if n.wait {
		select {
		case res := <-n.ch:
			n.emit(out, res)
		case <-time.After(selfupdate.CheckTimeout):
		}
		return
	}
	select {
	case res := <-n.ch:
		n.emit(out, res)
	default:
	}
}

// emit renders the answer in the form this invocation asked for.
func (n notice) emit(out io.Writer, res selfupdate.Result) {
	if n.status {
		fmt.Fprint(out, selfupdate.Status(res, runtime.GOOS))
		return
	}
	fmt.Fprint(out, selfupdate.Notice(res, runtime.GOOS))
}

// envAllowsUpdateCheck reads the kill switch. Anything that parses as false
// turns the check off; anything else, including an unset variable and a value
// that makes no sense, leaves it on.
func envAllowsUpdateCheck() bool {
	raw, ok := os.LookupEnv(updateCheckEnv)
	if !ok {
		return true
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return true
	}
	return value
}

// envForcesUpdateCheck reads the same variable's other edge: explicitly set
// and true. The default is a check nobody waits for — a suggestion is not
// worth a millisecond of anyone's time — but a variable someone set to 1 is a
// request for the answer, and racing the command against it would honor the
// letter of the check while losing its point.
func envForcesUpdateCheck() bool {
	raw, ok := os.LookupEnv(updateCheckEnv)
	if !ok {
		return false
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	return err == nil && value
}

// updateSource is where dispat looks for dispat.
//
// It is dispat's own repository, never the github block of the configuration:
// that block says where the user's packages are published, and asking it for
// dispat's release tags would come back empty forever. Only a flag the user
// explicitly set redirects it, which is what makes GitHub Enterprise work and
// what lets the tests point the whole thing at a local server.
func updateSource(o *options, fs *pflag.FlagSet) selfupdate.Source {
	var src selfupdate.Source
	if fs.Changed("owner") {
		src.Owner = *o.ghOwner
	}
	if fs.Changed("repo") {
		src.Repo = *o.ghRepo
	}
	if fs.Changed("api-url") {
		src.APIURL = *o.ghAPIURL
	}
	if fs.Changed("token-env") {
		src.Token = os.Getenv(*o.ghTokenEnv)
	} else {
		// The token is optional and only raises the rate limit, so the
		// conventional variable is worth trying when nothing named one.
		src.Token = os.Getenv("GITHUB_TOKEN")
	}
	return src
}
