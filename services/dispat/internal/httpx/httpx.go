// Package httpx makes the context and client timeout cover the whole HTTP
// exchange, including response reads and Close. TinyGo's transport does not
// observe either deadline, so abandoned exchanges are also bounded here.
package httpx

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

const (
	maxActiveRequests = 64
	maxReadChunk      = 32 << 10
)

// A timed-out TinyGo network operation cannot be forcibly stopped from this
// package. Its permit stays held until it really returns, so repeated timeouts
// cannot accumulate an unbounded number of goroutines and sockets.
var activeRequests = make(chan struct{}, maxActiveRequests)

type responseResult struct {
	response *http.Response
	err      error
}

type bodyOperation struct {
	data    []byte
	isClose bool
	done    chan bodyResult
}

type bodyResult struct {
	n   int
	err error
}

// guardedBody hands every operation to the one worker that owns the raw body.
// A Read receives its own buffer: after a deadline, the caller may reuse its
// buffer while TinyGo is still writing the late response into ours.
type guardedBody struct {
	ctx      context.Context
	ops      chan bodyOperation
	finished chan struct{}
	closed   atomic.Bool
}

func (b *guardedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	op := bodyOperation{data: make([]byte, min(len(p), maxReadChunk)), done: make(chan bodyResult, 1)}
	select {
	case b.ops <- op:
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.finished:
		return 0, io.ErrClosedPipe
	}
	select {
	case result := <-op.done:
		copy(p, op.data[:result.n])
		return result.n, result.err
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.finished:
		select {
		case result := <-op.done:
			copy(p, op.data[:result.n])
			return result.n, result.err
		default:
			return 0, io.ErrClosedPipe
		}
	}
}

func (b *guardedBody) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	op := bodyOperation{isClose: true, done: make(chan bodyResult, 1)}
	select {
	case b.ops <- op:
	case <-b.ctx.Done():
		return b.ctx.Err()
	case <-b.finished:
		return nil
	}
	select {
	case result := <-op.done:
		return result.err
	case <-b.ctx.Done():
		return b.ctx.Err()
	case <-b.finished:
		select {
		case result := <-op.done:
			return result.err
		default:
			return nil
		}
	}
}

// Do performs req and returns promptly when its context or client timeout
// ends. The effective deadline stays alive through response reads and Close.
// The worker that owns the transport also owns every body operation, so a
// transport that ignores cancellation can strand at most maxActiveRequests
// workers in this process, including ones stalled after response headers.
func Do(client *http.Client, req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	cancel := func() {}
	if client.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, client.Timeout)
	}
	if err := ctx.Err(); err != nil {
		cancel()
		return nil, contextError(req, err)
	}
	select {
	case activeRequests <- struct{}{}:
	case <-ctx.Done():
		cancel()
		return nil, contextError(req, ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		<-activeRequests
		cancel()
		return nil, contextError(req, err)
	}

	done := make(chan responseResult)
	go (&exchange{
		ctx: ctx, cancel: cancel, client: client, req: req.WithContext(ctx), done: done,
	}).run()
	select {
	case result := <-done:
		return result.response, result.err
	case <-ctx.Done():
		return nil, contextError(req, ctx.Err())
	}
}

type exchange struct {
	ctx    context.Context
	cancel context.CancelFunc
	client *http.Client
	req    *http.Request
	done   chan<- responseResult
}

func (x *exchange) run() {
	defer func() {
		x.cancel()
		<-activeRequests
	}()
	resp, err := x.client.Do(x.req)
	if err != nil || resp == nil {
		select {
		case x.done <- responseResult{resp, err}:
		case <-x.ctx.Done():
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
		return
	}

	rawBody := resp.Body
	guarded := &guardedBody{ctx: x.ctx, ops: make(chan bodyOperation), finished: make(chan struct{})}
	defer close(guarded.finished)
	resp.Body = guarded
	select {
	case x.done <- responseResult{response: resp}:
	case <-x.ctx.Done():
		_ = rawBody.Close()
		return
	}
	for {
		select {
		case op := <-guarded.ops:
			if op.isClose {
				op.done <- bodyResult{err: rawBody.Close()}
				return
			}
			n, err := rawBody.Read(op.data)
			op.done <- bodyResult{n: n, err: err}
		case <-x.ctx.Done():
			_ = rawBody.Close()
			return
		}
	}
}

func contextError(req *http.Request, err error) error {
	urlText := ""
	if req.URL != nil {
		urlText = req.URL.String()
	}
	return &url.Error{Op: op(req.Method), URL: urlText, Err: err}
}

// op spells a method the way net/http's own errors do: "Get", "Post".
func op(method string) string {
	if method == "" {
		return "Get"
	}
	return method[:1] + strings.ToLower(method[1:])
}
