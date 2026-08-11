package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

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
	return notice{ch: selfupdate.Check(ctx, updateSource(o, fs), build)}
}

// print writes the notice when the answer beat the command home.
func (n notice) print(out io.Writer) {
	select {
	case res := <-n.ch:
		if n.status {
			fmt.Fprint(out, selfupdate.Status(res, runtime.GOOS))
			return
		}
		fmt.Fprint(out, selfupdate.Notice(res, runtime.GOOS))
	default:
	}
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
