package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	returnTimeoutTestControlWindow          = 20 * time.Millisecond
	returnTimeoutTestTransferWindow         = 500 * time.Millisecond
	returnTimeoutTestExpiredWindow          = 80 * time.Millisecond
	returnTimeoutTestPastControl            = 80 * time.Millisecond
	returnTimeoutTestPastTransfer           = 200 * time.Millisecond
	returnTimeoutTestWaitLimit              = 2 * time.Second
	returnTimeoutTestDirectURL              = "https://return.invalid"
	returnTimeoutTestPayload                = "bounded result pack"
	returnTimeoutTestRun                    = "return-timeout-run"
	returnTimeoutTestGeneration             = "return-timeout-generation"
	returnTimeoutTestPlan                   = "return-timeout-plan"
	returnTimeoutTestGraph                  = "return-timeout-graph"
	returnTimeoutTestTask                   = "return-timeout-task"
	returnTimeoutTestNode                   = "return-timeout-node"
	returnTimeoutTestBranch                 = "return-timeout-branch"
	returnTimeoutTestClaim                  = "return-timeout-claim"
	returnTimeoutTestInputs                 = "return-timeout-inputs"
	returnTimeoutTestProposal               = "return-timeout-proposal"
	returnTimeoutTestResult                 = "return-timeout-result"
	returnTimeoutTestReceipt                = "return-timeout-receipt"
	returnTimeoutTestMaxDocumentBytes int64 = 1 << 20
)

type timedReturnResultBroker struct {
	*FirstNodeBroker
	entered  chan struct{}
	release  <-chan struct{}
	observed chan error
}

type gatedReturnResultBody struct {
	reader  io.Reader
	entered chan<- struct{}
	release <-chan struct{}
}

func (b *gatedReturnResultBody) Read(p []byte) (int, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return b.reader.Read(p)
}

func (b *gatedReturnResultBody) Close() error { return nil }

func (b *timedReturnResultBroker) AcceptResult(ctx context.Context,
	proposal ChoreographyResultProposal, _ *os.File) (ChoreographyReceipt, error) {
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	if err := ctx.Err(); err != nil {
		b.observed <- err
		return ChoreographyReceipt{}, err
	}
	b.observed <- nil
	query, err := FormatResultReceiptQuery(proposal)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	return ChoreographyReceipt{ChoreographyRunBinding: proposal.ChoreographyRunBinding,
		Kind: choreographyResultKind, Task: proposal.Task, Attempt: proposal.Attempt,
		Node: proposal.Node, Branch: proposal.Branch, ProposalID: proposal.ProposalID,
		ProposalDigest: query.ProposalDigest, OID: returnTimeoutTestReceipt,
		Accepted: true}, nil
}

func timedReturnResultRequest(t *testing.T, signer *Signer, baseURL string) *http.Request {
	t.Helper()
	payload := []byte(returnTimeoutTestPayload)
	payloadDigest := sha256.Sum256(payload)
	proposal := ChoreographyResultProposal{
		ChoreographyRunBinding: ChoreographyRunBinding{Run: returnTimeoutTestRun,
			Generation: returnTimeoutTestGeneration, PlanDigest: returnTimeoutTestPlan,
			GraphDigest: returnTimeoutTestGraph},
		Task: returnTimeoutTestTask, Attempt: 1, Node: returnTimeoutTestNode,
		Branch: returnTimeoutTestBranch, ClaimOID: returnTimeoutTestClaim,
		InputsDigest: returnTimeoutTestInputs, ProposalID: returnTimeoutTestProposal,
		ResultDigest: returnTimeoutTestResult, PayloadBytes: int64(len(payload)),
		PayloadSHA256: hex.EncodeToString(payloadDigest[:]),
	}
	document, err := json.Marshal(proposal)
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		baseURL+returnResultPath, bytes.NewReader(payload))
	require.NoError(t, err)
	request.Header.Set(returnDocumentHeader, base64.RawURLEncoding.EncodeToString(document))
	request.Header.Set(returnSignatureHeader, signer.Sign(returnResultKind, document))
	return request
}

func timedReturnResultHandler(t *testing.T, broker *timedReturnResultBroker,
	transferTimeout time.Duration) *choreographyReturnHandler {
	t.Helper()
	signer, err := NewSigner(returnTransportTestSecret)
	require.NoError(t, err)
	handler, err := NewChoreographyReturnHandler(ReturnHandlerOptions{
		Broker: broker, Signer: signer,
		Binding: ChoreographyRunBinding{Run: returnTimeoutTestRun,
			Generation: returnTimeoutTestGeneration, PlanDigest: returnTimeoutTestPlan,
			GraphDigest: returnTimeoutTestGraph},
		MaxClaimBytes:         returnTimeoutTestMaxDocumentBytes,
		MaxResultBytes:        int64(len(returnTimeoutTestPayload)),
		ResultTransferTimeout: transferTimeout, TempDir: t.TempDir()})
	require.NoError(t, err)
	return handler.(*choreographyReturnHandler)
}

func TestChoreographyReturnResultUsesTransferWindowBeyondControlDeadline(t *testing.T) {
	release := make(chan struct{})
	broker := &timedReturnResultBroker{entered: make(chan struct{}, 1),
		release: release, observed: make(chan error, 1)}
	handler := timedReturnResultHandler(t, broker, returnTimeoutTestTransferWindow)
	handler.controlTimeout = returnTimeoutTestControlWindow
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	signer, err := NewSigner(returnTransportTestSecret)
	require.NoError(t, err)
	request := timedReturnResultRequest(t, signer, server.URL)
	type response struct {
		value *http.Response
		err   error
	}
	done := make(chan response, 1)
	go func() {
		value, err := server.Client().Do(request)
		done <- response{value: value, err: err}
	}()
	select {
	case <-broker.entered:
	case <-time.After(returnTimeoutTestWaitLimit):
		t.Fatal("Result import did not begin")
	}
	time.Sleep(returnTimeoutTestPastControl)
	close(release)
	got := <-done
	require.NoError(t, got.err)
	defer got.value.Body.Close()
	require.Equal(t, http.StatusOK, got.value.StatusCode)
	require.NoError(t, <-broker.observed)
}

func TestChoreographyReturnResultTransferDeadlineLeavesUnknownReceipt(t *testing.T) {
	broker := &timedReturnResultBroker{entered: make(chan struct{}, 1),
		observed: make(chan error, 1)}
	handler := timedReturnResultHandler(t, broker, returnTimeoutTestExpiredWindow)
	handler.controlTimeout = returnTimeoutTestControlWindow
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	signer, err := NewSigner(returnTransportTestSecret)
	require.NoError(t, err)
	request := timedReturnResultRequest(t, signer, server.URL)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.ErrorIs(t, <-broker.observed, context.DeadlineExceeded)
}

func TestChoreographyReturnResultTransferDeadlineStartsBeforeSpool(t *testing.T) {
	broker := &timedReturnResultBroker{entered: make(chan struct{}, 1),
		observed: make(chan error, 1)}
	handler := timedReturnResultHandler(t, broker, returnTimeoutTestExpiredWindow)
	handler.controlTimeout = returnTimeoutTestControlWindow
	signer, err := NewSigner(returnTransportTestSecret)
	require.NoError(t, err)
	request := timedReturnResultRequest(t, signer, returnTimeoutTestDirectURL)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	request.Body = &gatedReturnResultBody{reader: bytes.NewReader([]byte(returnTimeoutTestPayload)),
		entered: entered, release: release}
	request.TLS = &tls.ConnectionState{}
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(returnTimeoutTestWaitLimit):
		t.Fatal("Result body was not read")
	}
	time.Sleep(returnTimeoutTestPastTransfer)
	close(release)
	select {
	case <-done:
	case <-time.After(returnTimeoutTestWaitLimit):
		t.Fatal("expired Result request did not return")
	}
	response := recorder.Result()
	defer response.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	select {
	case <-broker.entered:
		t.Fatal("expired transfer must not enter Result authority")
	default:
	}
}

type stalledReturnFeedBroker struct {
	*FirstNodeBroker
	deadlineError chan error
}

func (b *stalledReturnFeedBroker) ReadEvents(ctx context.Context,
	_ ChoreographyEventCursor, _ int) (ChoreographyEventPage, error) {
	<-ctx.Done()
	b.deadlineError <- ctx.Err()
	return ChoreographyEventPage{}, ctx.Err()
}

func TestChoreographyReturnFeedBoundsPersistenceWithoutClientCancellation(t *testing.T) {
	signer, err := NewSigner(returnTransportTestSecret)
	require.NoError(t, err)
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
		PlanDigest: "plan", GraphDigest: "graph"}
	broker := &stalledReturnFeedBroker{deadlineError: make(chan error, 1)}
	handler, err := NewChoreographyReturnHandler(ReturnHandlerOptions{Broker: broker,
		Signer: signer, Binding: binding, MaxClaimBytes: 1 << 20,
		MaxResultBytes: 1 << 20, TempDir: t.TempDir()})
	require.NoError(t, err)
	handler.(*choreographyReturnHandler).controlTimeout = 20 * time.Millisecond
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	document, err := json.Marshal(ChoreographyEventRequest{
		ChoreographyRunBinding: binding, Node: "peer", MaxEvents: 1})
	require.NoError(t, err)
	request, err := http.NewRequest(http.MethodPost, server.URL+returnEventsPath,
		bytes.NewReader(document))
	require.NoError(t, err)
	request.Header.Set(returnSignatureHeader, signer.Sign(returnEventsKind, document))
	client := server.Client()
	client.Timeout = time.Second
	response, err := client.Do(request)
	require.NoError(t, err, "the server bounds the stalled ledger while the client remains connected")
	defer response.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.ErrorIs(t, <-broker.deadlineError, context.DeadlineExceeded)
	require.NoError(t, request.Context().Err())
}
