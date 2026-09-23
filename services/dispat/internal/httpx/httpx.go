// Package httpx holds the one guarantee every HTTP call the CLI makes relies
// on and not every net/http gives: a request ends when its context does.
//
// The standard transport already behaves that way. The TinyGo build's does
// not. Its client dials, writes the request and reads the response on the
// calling goroutine, and consults neither the request's context nor the
// client's Timeout, so a server that accepts a request and then never answers
// would hold a release for as long as it liked, and an interrupt could not free
// it. Every request goes through Do so that both builds keep the same promise.
package httpx

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Do performs req with client and returns no later than the moment the
// request's context ends or the client's Timeout elapses, whichever comes
// first, whether or not the transport underneath notices either.
//
// An ending reported here has the shape net/http gives its own: a *url.Error
// carrying the context's error, so a caller asking errors.Is(err,
// context.Canceled) or for a Timeout gets the answer it would from the
// standard client. The abandoned round trip is left to finish on its own, and
// a response it eventually produces is closed rather than leaked.
func Do(client *http.Client, req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if client.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, client.Timeout)
		defer cancel()
	}

	type result struct {
		resp *http.Response
		err  error
	}
	// An unbuffered handoff gives the response exactly one owner. If the
	// caller has left on cancellation, this same goroutine closes a late
	// response instead of leaving a second goroutine waiting for it.
	done := make(chan result)
	go func() {
		resp, err := client.Do(req)
		select {
		case done <- result{resp, err}:
		case <-ctx.Done():
			if resp != nil {
				_ = resp.Body.Close()
			}
		}
	}()

	select {
	case r := <-done:
		return r.resp, r.err
	case <-ctx.Done():
		return nil, &url.Error{Op: op(req.Method), URL: req.URL.String(), Err: ctx.Err()}
	}
}

// op spells a method the way net/http's own errors do: "Get", "Post".
func op(method string) string {
	if method == "" {
		return "Get"
	}
	return method[:1] + strings.ToLower(method[1:])
}
