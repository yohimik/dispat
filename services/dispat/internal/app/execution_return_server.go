package app

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

type runReturnServer struct {
	server   *http.Server
	done     chan error
	once     sync.Once
	closeErr error
	address  string
}

// startRunReturnServer binds before preflight, so the signed endpoint sent to
// peers never describes a listener that failed to start. Its lifetime is
// owned by the caller, not the command context: a cancelled command may
// still owe a peer signed Cancel/Result/Ack settlement through this listener.
// The caller closes it after the coordinator has settled or retained refs.
func startRunReturnServer(listen string, cert tls.Certificate, handler http.Handler) (*runReturnServer, error) {
	listener, err := net.Listen(runReturnNetworkName, listen)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: runReturnReadHeaderTimeoutSeconds * time.Second,
		MaxHeaderBytes:    runReturnMaximumHeaderBytes,
	}
	server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	state := &runReturnServer{server: server, done: make(chan error, runReturnCompletionBufferCount), address: listener.Addr().String()}
	go func() { state.done <- server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	return state, nil
}

func (s *runReturnServer) close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		err := s.server.Shutdown(ctx)
		served := <-s.done
		if err != nil {
			s.closeErr = err
		} else if served != nil && !errors.Is(served, http.ErrServerClosed) {
			s.closeErr = served
		}
	})
	return s.closeErr
}
