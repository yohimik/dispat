package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func TestRunReturnGateRefusesUntilBound(t *testing.T) {
	t.Parallel()
	gate := &runReturnGate{}
	before := httptest.NewRecorder()
	gate.ServeHTTP(before, httptest.NewRequest(http.MethodPost, "/claim", nil))
	if before.Code != http.StatusServiceUnavailable {
		t.Fatalf("unbound return gate: status %d", before.Code)
	}
	gate.bind(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	after := httptest.NewRecorder()
	gate.ServeHTTP(after, httptest.NewRequest(http.MethodPost, "/claim", nil))
	if after.Code != http.StatusAccepted {
		t.Fatalf("bound return gate: status %d", after.Code)
	}
}

func TestDistributedReturnChannelRequiresAnEndpoint(t *testing.T) {
	t.Parallel()
	a := &App{cfg: &config.File{Execution: &config.ExecutionConfig{
		Workers: []config.ExecutionWorkerConfig{{Name: "second"}},
	}}}
	_, err := a.openRunReturnChannel("", "")
	if err == nil {
		t.Fatal("distributed run accepted no return endpoint")
	}
}
