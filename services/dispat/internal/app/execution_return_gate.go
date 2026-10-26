package app

import (
	"net/http"
	"sync"
)

// runReturnGate permits the listener to be advertised during preflight while
// refusing proposals until the task refs and broker have been initialized.
type runReturnGate struct {
	mu      sync.RWMutex
	handler http.Handler
}

func (g *runReturnGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.RLock()
	handler := g.handler
	g.mu.RUnlock()
	if handler == nil {
		http.Error(w, runReturnNotReadyErrorText, http.StatusServiceUnavailable)
		return
	}
	handler.ServeHTTP(w, r)
}

func (g *runReturnGate) bind(handler http.Handler) {
	g.mu.Lock()
	g.handler = handler
	g.mu.Unlock()
}
