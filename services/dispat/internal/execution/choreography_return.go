// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The direct, non-Git return channel to the first node. The app owns the
// listener and its short-lived TLS certificate; the client pins that exact
// certificate from the run's signed preflight. This layer never starts a
// listener or grants a choreographer a Git push capability.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	public "github.com/yohimik/dispat/pkg/models"
)

const (
	returnDocumentHeader                                = "X-Dispat-Document"
	returnSignatureHeader                               = "X-Dispat-Signature"
	returnReplyLimit                                    = 64 << 10
	returnEventPageLimit                                = 1 << 20
	returnHandlerIncompleteError                        = "execution: incomplete choreography return handler"
	returnTLSRequiredMessage                            = "TLS required"
	returnEventCursorInvalidMessage                     = "invalid event cursor"
	returnEventFeedUnavailableMessage                   = "event feed unavailable"
	returnContentTypeHeader                             = "Content-Type"
	returnJSONMediaType                                 = "application/json"
	returnSingleEventOversizeError                      = "execution: single signed event exceeds transfer limit"
	returnEventPageBoundError                           = "execution: invalid event page bound"
	returnAckInvalidMessage                             = "invalid cancellation acknowledgement"
	returnClaimInvalidMessage                           = "invalid claim proposal"
	returnResultInvalidMessage                          = "invalid result proposal"
	returnResultTempPattern                             = "dispat-result-*"
	returnResultStagingUnavailableMessage               = "result staging unavailable"
	returnResultLengthMismatchMessage                   = "result bytes do not match proposal"
	returnReceiptQueryInvalidMessage                    = "invalid receipt query"
	returnReceiptStatusUnknownMessage                   = "receipt status unknown"
	returnReceiptUnsettledMessage                       = "receipt not yet settled"
	returnDocumentOversizeError                         = "execution: return document exceeds limit"
	returnDocumentSignatureError                        = "execution: return document signature invalid"
	returnDocumentTrailingError                         = "execution: trailing return document"
	returnProposalUnsettledMessage                      = "proposal not settled"
	returnPersistedReceiptInvalidMessage                = "invalid persisted receipt"
	returnReceiptEncodingMessage                        = "receipt encoding failed"
	returnHTTPScheme                                    = "https"
	returnEndpointInvalidError                          = "execution: invalid choreography return endpoint"
	returnTLSIdentityInvalidError                       = "execution: invalid choreography TLS identity"
	returnCertificateMissingError                       = "execution: return endpoint supplied no certificate"
	returnCertificateMismatchError                      = "execution: return endpoint certificate differs from signed preflight"
	returnURLRootPath                                   = "/"
	returnRedirectRefusedError                          = "execution: return endpoint redirects are refused"
	returnProbeReplyRunMismatchError                    = "execution: probe reply does not match pinned run"
	returnProbeReceiptMismatchError                     = "execution: probe receipt differs from reply"
	returnAckIdentityMissingError                       = "execution: cancellation acknowledgement lacks identity"
	returnAckReceiptMismatchError                       = "execution: cancellation acknowledgement receipt differs"
	returnClaimRunMismatchError                         = "execution: claim does not match pinned run"
	returnClaimReceiptMismatchError                     = "execution: claim receipt differs from proposal"
	returnResultPayloadLimitError                       = "execution: invalid result payload limit"
	returnResultRewindErrorFormat                       = "execution: rewinding result payload: %w"
	returnResultHashErrorFormat                         = "execution: hashing result payload: %w"
	returnResultPayloadOversizeError                    = "execution: result payload exceeds limit"
	returnResultRunMismatchError                        = "execution: result does not match pinned run"
	returnResultPayloadLengthChangedError               = "execution: prepared result payload length changed"
	returnResultReceiptMismatchError                    = "execution: result receipt differs from proposal"
	returnReceiptQueryIdentityError                     = "execution: receipt query lacks identity"
	returnQueriedReceiptIdentityError                   = "execution: queried receipt differs from proposal identity"
	returnEventRequestInvalidError                      = "execution: invalid event request"
	returnEventResponseUnknownFormat                    = "execution: event response unknown: %w"
	returnEventFeedStatusFormat                         = "execution: event feed unavailable (HTTP %d)"
	returnEventPageSignatureError                       = "execution: event page signature or size invalid"
	returnEventPageContiguityError                      = "execution: event page is not contiguous"
	returnEventPageStateError                           = "execution: event page contains invalid state"
	returnReceiptNotFoundError                          = "execution: proposal has no settled receipt"
	returnResponseUnknownFormat                         = "execution: return response unknown: %w"
	returnResponseUnsettledFormat                       = "execution: return response unsettled (HTTP %d)"
	returnReceiptSignatureError                         = "execution: return receipt signature or size invalid"
	returnReceiptRunMismatchError                       = "execution: return receipt does not match pinned run"
	returnMissingIdentity                               = ""
	returnMinimumPositiveBytes            int64         = 1
	returnMinimumTaskAttempt                            = 1
	returnMinimumEventPageSize                          = 1
	returnMaximumEventPageSize                          = 32
	returnEventPageHalvingDivisor                       = 2
	returnReplyReadOverflowBytes          int64         = 1
	returnFirstCertificateIndex                         = 0
	returnNoEvents                                      = 0
	returnNoFenceSequence                 uint64        = 0
	returnNoCertificates                                = 0
	returnSeekStartOffset                 int64         = 0
	returnReadNoBytes                                   = 0
	returnEventNextSequence               uint64        = 1
	returnNoReceipt                                     = false
	returnPinnedCertificateVerifier                     = true
	returnNoTransferTimeout               time.Duration = 0
)

// ChoreographyReturnBrokerx owns semantic validation and Git persistence on
// the first node. LookupReceipt returns only receipts durably settled by the
// same broker; a lost HTTP response must never cause a second effect.
type ChoreographyReturnBrokerx interface {
	AcceptClaim(context.Context, ChoreographyClaimProposal) (ChoreographyReceipt, error)
	AcceptResult(context.Context, ChoreographyResultProposal, *os.File) (ChoreographyReceipt, error)
	AcceptAck(context.Context, ChoreographyAckProposal) (ChoreographyReceipt, error)
	LookupReceipt(context.Context, ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error)
	ReadEvents(context.Context, ChoreographyEventCursor, int) (ChoreographyEventPage, error)
}

type ChoreographyJoinBrokerx interface {
	AdmitNode(context.Context, ChoreographyJoinProposal) (ChoreographyJoinReceipt, error)
	LookupJoinReceipt(context.Context, ChoreographyJoinReceiptQuery) (ChoreographyJoinReceipt, bool, error)
}

type ChoreographyDrainBrokerx interface {
	DrainNode(context.Context, ChoreographyDrainProposal) (ChoreographyDrainReceipt, error)
	LookupDrainReceipt(context.Context, ChoreographyDrainReceiptQuery) (ChoreographyDrainReceipt, bool, error)
}

// ReturnHandlerOptions configures one run's handler. MaxResultBytes is the
// existing transfer budget, not an in-memory allocation. TempDir must be a
// first-node-owned directory on the same machine as its object store.
type ReturnHandlerOptions struct {
	Broker                ChoreographyReturnBrokerx
	Signer                *Signer
	Binding               ChoreographyRunBinding
	MaxClaimBytes         int64
	MaxResultBytes        int64
	ResultTransferTimeout time.Duration
	TempDir               string
}

// NewChoreographyReturnHandler returns a handler, not a listener. Its caller
// must serve it only on TLS with the exact per-run identity advertised in the
// signed preflight. Every request is independently HMAC-authenticated.
func NewChoreographyReturnHandler(opts ReturnHandlerOptions) (http.Handler, error) {
	if opts.Broker == nil || opts.Signer == nil || !opts.Binding.valid() ||
		opts.MaxClaimBytes < returnMinimumPositiveBytes || opts.MaxResultBytes < returnMinimumPositiveBytes ||
		opts.ResultTransferTimeout < returnNoTransferTimeout || opts.TempDir == returnMissingIdentity {
		return nil, fmt.Errorf(returnHandlerIncompleteError)
	}
	if opts.ResultTransferTimeout == returnNoTransferTimeout {
		opts.ResultTransferTimeout = time.Duration(public.DefaultExecutionTransferTimeout) * time.Second
	}
	return &choreographyReturnHandler{opts: opts, controlTimeout: taskReportTimeout}, nil
}

type choreographyReturnHandler struct {
	opts           ReturnHandlerOptions
	controlTimeout time.Duration
}

func (h *choreographyReturnHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil {
		http.Error(w, returnTLSRequiredMessage, http.StatusUpgradeRequired)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == returnResultPath {
		h.result(w, r)
		return
	}
	// A peer can keep its connection alive after the run stops. Its ledger
	// read must not hold the persistence lock past bounded local cleanup.
	ctx, cancel := context.WithTimeout(r.Context(), h.controlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	switch {
	case r.Method == http.MethodPost && r.URL.Path == returnDrainPath:
		h.drain(w, r)
	case r.Method == http.MethodPost && r.URL.Path == returnDrainReceiptPath:
		h.drainReceipt(w, r)
	case r.Method == http.MethodPost && r.URL.Path == returnJoinPath:
		h.join(w, r)
	case r.Method == http.MethodPost && r.URL.Path == returnJoinReceiptPath:
		h.joinReceipt(w, r)
	case r.Method == http.MethodPost && r.URL.Path == returnClaimPath:
		h.claim(w, r)
	case r.Method == http.MethodPost && r.URL.Path == returnAckPath:
		h.ack(w, r)
	case r.Method == http.MethodPost && r.URL.Path == returnReceiptPath:
		h.receipt(w, r)
	case r.Method == http.MethodPost && r.URL.Path == returnEventsPath:
		h.events(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *choreographyReturnHandler) events(w http.ResponseWriter, r *http.Request) {
	var request ChoreographyEventRequest
	if err := h.readSignedJSON(choreographySignedJSONRead{Request: r, Kind: returnEventsKind, MaxBytes: h.opts.MaxClaimBytes, Out: &request}); err != nil ||
		request.ChoreographyRunBinding != h.opts.Binding || request.Node == returnMissingIdentity ||
		request.MaxEvents < returnMinimumEventPageSize || request.MaxEvents > returnMaximumEventPageSize {
		http.Error(w, returnEventCursorInvalidMessage, http.StatusBadRequest)
		return
	}
	document, err := encodeBoundedEventPage(r.Context(), h.opts.Broker.ReadEvents,
		request.Cursor, request.MaxEvents)
	if err != nil {
		http.Error(w, returnEventFeedUnavailableMessage, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set(returnContentTypeHeader, returnJSONMediaType)
	w.Header().Set(returnSignatureHeader, h.opts.Signer.Sign(returnPageKind, document))
	_, _ = w.Write(document)
}

func encodeBoundedEventPage(ctx context.Context,
	read func(context.Context, ChoreographyEventCursor, int) (ChoreographyEventPage, error),
	cursor ChoreographyEventCursor, maxEvents int) ([]byte, error) {
	for limit := maxEvents; limit >= returnMinimumEventPageSize; limit /= returnEventPageHalvingDivisor {
		page, err := read(ctx, cursor, limit)
		if err != nil {
			return nil, err
		}
		document, err := json.Marshal(page)
		if err != nil {
			return nil, err
		}
		if len(document) <= returnEventPageLimit {
			return document, nil
		}
		if limit == returnMinimumEventPageSize {
			return nil, fmt.Errorf(returnSingleEventOversizeError)
		}
	}
	return nil, fmt.Errorf(returnEventPageBoundError)
}

func (h *choreographyReturnHandler) ack(w http.ResponseWriter, r *http.Request) {
	var proposal ChoreographyAckProposal
	if err := h.readSignedJSON(choreographySignedJSONRead{Request: r, Kind: returnAckKind, MaxBytes: h.opts.MaxClaimBytes, Out: &proposal}); err != nil ||
		proposal.ChoreographyRunBinding != h.opts.Binding || proposal.Task == returnMissingIdentity || proposal.Attempt < returnMinimumTaskAttempt ||
		proposal.Node == returnMissingIdentity || proposal.Branch == returnMissingIdentity || proposal.CancelOID == returnMissingIdentity ||
		proposal.AssignmentOID == returnMissingIdentity || proposal.ProposalID == returnMissingIdentity {
		http.Error(w, returnAckInvalidMessage, http.StatusBadRequest)
		return
	}
	receipt, err := h.opts.Broker.AcceptAck(r.Context(), proposal)
	h.writeReceipt(w, receipt, err)
}

func (h *choreographyReturnHandler) claim(w http.ResponseWriter, r *http.Request) {
	var proposal ChoreographyClaimProposal
	if err := h.readSignedJSON(choreographySignedJSONRead{Request: r, Kind: returnClaimKind, MaxBytes: h.opts.MaxClaimBytes, Out: &proposal}); err != nil ||
		proposal.validate() != nil || proposal.ChoreographyRunBinding != h.opts.Binding {
		http.Error(w, returnClaimInvalidMessage, http.StatusBadRequest)
		return
	}
	receipt, err := h.opts.Broker.AcceptClaim(r.Context(), proposal)
	h.writeReceipt(w, receipt, err)
}

func (h *choreographyReturnHandler) result(w http.ResponseWriter, r *http.Request) {
	// The worker's configured transfer window covers the upload and the
	// first node's pack import and verification as one request.
	ctx, cancel := context.WithTimeout(r.Context(), max(h.controlTimeout, h.opts.ResultTransferTimeout))
	defer cancel()
	r = r.WithContext(ctx)
	document, err := base64.RawURLEncoding.DecodeString(r.Header.Get(returnDocumentHeader))
	if err != nil || int64(len(document)) > h.opts.MaxClaimBytes ||
		!h.opts.Signer.IsSignatureValid(returnResultKind, document, r.Header.Get(returnSignatureHeader)) {
		http.Error(w, returnResultInvalidMessage, http.StatusBadRequest)
		return
	}
	var proposal ChoreographyResultProposal
	if err := strictJSON(document, &proposal); err != nil ||
		proposal.validate(h.opts.MaxResultBytes) != nil || proposal.ChoreographyRunBinding != h.opts.Binding {
		http.Error(w, returnResultInvalidMessage, http.StatusBadRequest)
		return
	}
	// Spool before import. A forged or truncated body cannot enter Git's object
	// store, and even a maximum-sized result uses constant process memory.
	tmp, err := os.CreateTemp(h.opts.TempDir, returnResultTempPattern)
	if err != nil {
		http.Error(w, returnResultStagingUnavailableMessage, http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	digest := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, digest),
		&contextReader{ctx: ctx, from: io.LimitReader(r.Body, proposal.PayloadBytes+returnReplyReadOverflowBytes)})
	if ctx.Err() != nil {
		h.writeReceipt(w, ChoreographyReceipt{}, ctx.Err())
		return
	}
	if copyErr != nil || n != proposal.PayloadBytes ||
		hex.EncodeToString(digest.Sum(nil)) != proposal.PayloadSHA256 {
		http.Error(w, returnResultLengthMismatchMessage, http.StatusBadRequest)
		return
	}
	if _, err := tmp.Seek(returnSeekStartOffset, io.SeekStart); err != nil {
		http.Error(w, returnResultStagingUnavailableMessage, http.StatusServiceUnavailable)
		return
	}
	receipt, err := h.opts.Broker.AcceptResult(ctx, proposal, tmp)
	h.writeReceipt(w, receipt, err)
}

func (h *choreographyReturnHandler) receipt(w http.ResponseWriter, r *http.Request) {
	var query ChoreographyReceiptQuery
	if err := h.readSignedJSON(choreographySignedJSONRead{Request: r, Kind: returnQueryKind, MaxBytes: h.opts.MaxClaimBytes, Out: &query}); err != nil ||
		query.ChoreographyRunBinding != h.opts.Binding ||
		(query.Kind != choreographyClaimKind && query.Kind != choreographyResultKind && query.Kind != MessageAck) || query.Task == returnMissingIdentity ||
		query.Attempt < returnMinimumTaskAttempt || query.Branch == returnMissingIdentity || query.Node == returnMissingIdentity || query.ProposalID == returnMissingIdentity || query.ProposalDigest == returnMissingIdentity ||
		(query.Kind == choreographyClaimKind && query.GraphRootOID == returnMissingIdentity) {
		http.Error(w, returnReceiptQueryInvalidMessage, http.StatusBadRequest)
		return
	}
	receipt, found, err := h.opts.Broker.LookupReceipt(r.Context(), query)
	if err != nil {
		http.Error(w, returnReceiptStatusUnknownMessage, http.StatusServiceUnavailable)
		return
	}
	if !found {
		http.Error(w, returnReceiptUnsettledMessage, http.StatusNotFound)
		return
	}
	h.writeReceipt(w, receipt, nil)
}

type choreographySignedJSONRead struct {
	Request  *http.Request
	Kind     MessageKind
	MaxBytes int64
	Out      any
}

func (h *choreographyReturnHandler) readSignedJSON(input choreographySignedJSONRead) error {
	data, err := io.ReadAll(io.LimitReader(input.Request.Body, input.MaxBytes+returnReplyReadOverflowBytes))
	if err != nil || int64(len(data)) > input.MaxBytes {
		return fmt.Errorf(returnDocumentOversizeError)
	}
	if !h.opts.Signer.IsSignatureValid(input.Kind, data, input.Request.Header.Get(returnSignatureHeader)) {
		return fmt.Errorf(returnDocumentSignatureError)
	}
	return strictJSON(data, input.Out)
}

func strictJSON(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf(returnDocumentTrailingError)
	}
	return nil
}

func (h *choreographyReturnHandler) writeReceipt(w http.ResponseWriter, receipt ChoreographyReceipt, err error) {
	if err != nil && !(errors.Is(err, ErrChoreographyBindingFatal) && receipt.Refused &&
		receipt.RefusalReason == ChoreographyRefusalStopping) {
		// An unknown Git outcome is not a rejection. The client queries the
		// proposal ID before considering any retry.
		http.Error(w, returnProposalUnsettledMessage, http.StatusServiceUnavailable)
		return
	}
	if receipt.ChoreographyRunBinding != h.opts.Binding || receipt.Kind == returnMissingIdentity ||
		receipt.ProposalID == returnMissingIdentity ||
		(receipt.Task != PreflightTask && receipt.ProposalDigest == returnMissingIdentity) || receipt.OID == returnMissingIdentity {
		http.Error(w, returnPersistedReceiptInvalidMessage, http.StatusInternalServerError)
		return
	}
	data, marshalErr := json.Marshal(receipt)
	if marshalErr != nil {
		http.Error(w, returnReceiptEncodingMessage, http.StatusInternalServerError)
		return
	}
	w.Header().Set(returnContentTypeHeader, returnJSONMediaType)
	w.Header().Set(returnSignatureHeader, h.opts.Signer.Sign(returnReplyKind, data))
	_, _ = w.Write(data)
}

// ReturnClientOptions binds an HTTPS client to the first node's exact run
// certificate. The pin is the lowercase SHA-256 of the leaf certificate DER.
type ReturnClientOptions struct {
	BaseURL          string
	PinnedCertSHA256 string
	Signer           *Signer
	Binding          ChoreographyRunBinding
	MaxResultBytes   int64
}

type ChoreographyReturnClient struct {
	base           string
	client         *http.Client
	signer         *Signer
	binding        ChoreographyRunBinding
	maxResultBytes int64
}

type returnRequest struct {
	path          string
	kind          MessageKind
	document      []byte
	body          io.Reader
	contentLength *int64
}

// NewChoreographyReturnClient refuses plaintext and any server certificate
// other than the one signed into this run's preflight. The per-run HMAC key
// authenticates requests and first-node receipts at the protocol layer.
func NewChoreographyReturnClient(opts ReturnClientOptions) (*ChoreographyReturnClient, error) {
	u, err := url.Parse(opts.BaseURL)
	if err != nil || u.Scheme != returnHTTPScheme || u.Host == returnMissingIdentity || u.User != nil ||
		u.RawQuery != returnMissingIdentity || u.Fragment != returnMissingIdentity || (u.Path != returnMissingIdentity && u.Path != returnURLRootPath) ||
		opts.Signer == nil || !opts.Binding.valid() || opts.MaxResultBytes < returnMinimumPositiveBytes {
		return nil, fmt.Errorf(returnEndpointInvalidError)
	}
	pin, err := hex.DecodeString(opts.PinnedCertSHA256)
	if err != nil || len(pin) != sha256.Size || opts.PinnedCertSHA256 != strings.ToLower(opts.PinnedCertSHA256) {
		return nil, fmt.Errorf(returnTLSIdentityInvalidError)
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// The signed exact leaf pin is the trust root for this per-run,
		// possibly self-signed certificate.
		InsecureSkipVerify: returnPinnedCertificateVerifier, //nolint:gosec
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == returnNoCertificates {
				return fmt.Errorf(returnCertificateMissingError)
			}
			sum := sha256.Sum256(raw[returnFirstCertificateIndex])
			if !bytes.Equal(sum[:], pin) {
				return fmt.Errorf(returnCertificateMismatchError)
			}
			return nil
		},
	}
	return &ChoreographyReturnClient{base: strings.TrimSuffix(opts.BaseURL, returnURLRootPath),
		client: &http.Client{Transport: newPinnedReturnTransport(u.Host, tlsConfig), CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf(returnRedirectRefusedError)
		}}, signer: opts.Signer, binding: opts.Binding, maxResultBytes: opts.MaxResultBytes}, nil
}

// Close releases idle connections to this run's endpoint after the run ends.
func (c *ChoreographyReturnClient) Close() { c.client.CloseIdleConnections() }

// SubmitProbe returns this node's report through the first-node-only Git
// writer. A lost reply may be retried with the same assignment OID: the
// first node reads its probe ref and returns the already persisted result.
func (c *ChoreographyReturnClient) SubmitProbe(ctx context.Context, reply ChoreographyProbeReply) (ChoreographyReceipt, error) {
	if reply.ChoreographyRunBinding != c.binding || reply.Node == returnMissingIdentity || reply.Branch == returnMissingIdentity || reply.AssignmentOID == returnMissingIdentity {
		return ChoreographyReceipt{}, fmt.Errorf(returnProbeReplyRunMismatchError)
	}
	document, err := json.Marshal(reply)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	receipt, err := c.send(ctx, returnRequest{path: returnProbePath, kind: returnProbeKind,
		document: document, body: bytes.NewReader(document)})
	if err == nil && (receipt.Kind != MessageResult || receipt.Task != PreflightTask ||
		receipt.Node != reply.Node || receipt.Branch != reply.Branch || receipt.ProposalID != reply.AssignmentOID) {
		return ChoreographyReceipt{}, fmt.Errorf(returnProbeReceiptMismatchError)
	}
	return receipt, err
}

// SubmitAck asks the first node to persist an acknowledgement of the exact
// cancellation. Unknown responses are settled by querying the proposal ID.
func (c *ChoreographyReturnClient) SubmitAck(ctx context.Context, p ChoreographyAckProposal) (ChoreographyReceipt, error) {
	if p.ChoreographyRunBinding != c.binding || p.Task == returnMissingIdentity || p.Attempt < returnMinimumTaskAttempt || p.Node == returnMissingIdentity ||
		p.Branch == returnMissingIdentity || p.CancelOID == returnMissingIdentity || p.AssignmentOID == returnMissingIdentity || p.ProposalID == returnMissingIdentity {
		return ChoreographyReceipt{}, fmt.Errorf(returnAckIdentityMissingError)
	}
	document, err := json.Marshal(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	query, err := FormatAckReceiptQuery(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	receipt, err := c.send(ctx, returnRequest{path: returnAckPath, kind: returnAckKind,
		document: document, body: bytes.NewReader(document)})
	if err == nil && (receipt.Kind != MessageAck || receipt.Task != p.Task || receipt.Attempt != p.Attempt ||
		receipt.Node != p.Node || receipt.Branch != p.Branch || receipt.ProposalID != p.ProposalID ||
		receipt.ProposalDigest != query.ProposalDigest) {
		return ChoreographyReceipt{}, fmt.Errorf(returnAckReceiptMismatchError)
	}
	return receipt, err
}

// SubmitClaim sends a selection to the first node. A transport error or a
// non-success reply leaves its status unknown; callers query the proposal ID
// instead of assuming rejection or running without a persisted receipt.
func (c *ChoreographyReturnClient) SubmitClaim(ctx context.Context, proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
	if err := proposal.validate(); err != nil || proposal.ChoreographyRunBinding != c.binding {
		return ChoreographyReceipt{}, fmt.Errorf(returnClaimRunMismatchError)
	}
	document, err := json.Marshal(proposal)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	query, err := FormatClaimReceiptQuery(proposal)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	receipt, err := c.send(ctx, returnRequest{path: returnClaimPath, kind: returnClaimKind,
		document: document, body: bytes.NewReader(document)})
	if err == nil && (receipt.Kind != choreographyClaimKind || receipt.Task != proposal.Task || receipt.Attempt != proposal.Attempt ||
		receipt.Node != proposal.Node || receipt.Branch != proposal.Branch || receipt.ProposalID != proposal.ProposalID ||
		receipt.ProposalDigest != query.ProposalDigest) {
		return ChoreographyReceipt{}, fmt.Errorf(returnClaimReceiptMismatchError)
	}
	return receipt, err
}

// PrepareChoreographyResult computes the exact bounded body identity before
// transmission. Callers retain both the prepared proposal and payload for
// ambiguous-response reconciliation; no Git effect occurs here.
func PrepareChoreographyResult(ctx context.Context, proposal ChoreographyResultProposal,
	payload io.ReadSeeker, maxBytes int64) (ChoreographyResultProposal, error) {
	if payload == nil || maxBytes < returnMinimumPositiveBytes || maxBytes == math.MaxInt64 {
		return ChoreographyResultProposal{}, fmt.Errorf(returnResultPayloadLimitError)
	}
	if _, err := payload.Seek(returnSeekStartOffset, io.SeekStart); err != nil {
		return ChoreographyResultProposal{}, fmt.Errorf(returnResultRewindErrorFormat, err)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, from: payload}, maxBytes+returnReplyReadOverflowBytes))
	if err != nil {
		return ChoreographyResultProposal{}, fmt.Errorf(returnResultHashErrorFormat, err)
	}
	if n > maxBytes {
		return ChoreographyResultProposal{}, fmt.Errorf(returnResultPayloadOversizeError)
	}
	proposal.PayloadBytes = n
	proposal.PayloadSHA256 = hex.EncodeToString(hash.Sum(nil))
	if err := proposal.validate(maxBytes); err != nil {
		return ChoreographyResultProposal{}, err
	}
	if _, err := payload.Seek(returnSeekStartOffset, io.SeekStart); err != nil {
		return ChoreographyResultProposal{}, fmt.Errorf(returnResultRewindErrorFormat, err)
	}
	return proposal, nil
}

// SubmitResult streams an already prepared exact payload without buffering
// it in process memory. The caller retains it until a receipt settles.
func (c *ChoreographyReturnClient) SubmitResult(ctx context.Context, proposal ChoreographyResultProposal,
	payload io.ReadSeeker) (ChoreographyReceipt, error) {
	if payload == nil || proposal.ChoreographyRunBinding != c.binding ||
		proposal.validate(c.maxResultBytes) != nil {
		return ChoreographyReceipt{}, fmt.Errorf(returnResultRunMismatchError)
	}
	n, err := payload.Seek(returnSeekStartOffset, io.SeekEnd)
	if err != nil || n != proposal.PayloadBytes {
		return ChoreographyReceipt{}, fmt.Errorf(returnResultPayloadLengthChangedError)
	}
	if _, err := payload.Seek(returnSeekStartOffset, io.SeekStart); err != nil {
		return ChoreographyReceipt{}, fmt.Errorf(returnResultRewindErrorFormat, err)
	}
	document, err := json.Marshal(proposal)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	query, err := FormatResultReceiptQuery(proposal)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	receipt, err := c.send(ctx, returnRequest{path: returnResultPath, kind: returnResultKind,
		document: document, body: payload, contentLength: &n})
	if err == nil && (receipt.Kind != choreographyResultKind || receipt.Task != proposal.Task || receipt.Attempt != proposal.Attempt ||
		receipt.Node != proposal.Node || receipt.Branch != proposal.Branch || receipt.ProposalID != proposal.ProposalID ||
		receipt.ProposalDigest != query.ProposalDigest) {
		return ChoreographyReceipt{}, fmt.Errorf(returnResultReceiptMismatchError)
	}
	return receipt, err
}

// LookupReceipt settles an unanswered claim or result response without
// replaying a possibly nonidempotent task or retransmitting its artifact.
func (c *ChoreographyReturnClient) LookupReceipt(ctx context.Context, query ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
	if query.ChoreographyRunBinding != c.binding ||
		(query.Kind != choreographyClaimKind && query.Kind != choreographyResultKind && query.Kind != MessageAck) ||
		query.Task == returnMissingIdentity || query.Attempt < returnMinimumTaskAttempt ||
		query.Branch == returnMissingIdentity || query.Node == returnMissingIdentity || query.ProposalID == returnMissingIdentity || query.ProposalDigest == returnMissingIdentity {
		return ChoreographyReceipt{}, returnNoReceipt, fmt.Errorf(returnReceiptQueryIdentityError)
	}
	document, err := json.Marshal(query)
	if err != nil {
		return ChoreographyReceipt{}, returnNoReceipt, err
	}
	receipt, err := c.send(ctx, returnRequest{path: returnReceiptPath, kind: returnQueryKind,
		document: document, body: bytes.NewReader(document)})
	if errors.Is(err, errReceiptNotFound) {
		return ChoreographyReceipt{}, returnNoReceipt, nil
	}
	if err == nil && (receipt.Kind != query.Kind || receipt.Task != query.Task || receipt.Attempt != query.Attempt ||
		receipt.Branch != query.Branch || receipt.Node != query.Node || receipt.ProposalID != query.ProposalID ||
		receipt.ProposalDigest != query.ProposalDigest) {
		return ChoreographyReceipt{}, returnNoReceipt, fmt.Errorf(returnQueriedReceiptIdentityError)
	}
	return receipt, err == nil, err
}

// PollEvents returns the earliest accepted frontier changes after the
// caller's exact durable cursor. The page is signed by the first node; a peer
// applies it once in sequence and persists the returned cursor on reconnect.
func (c *ChoreographyReturnClient) PollEvents(ctx context.Context,
	request ChoreographyEventRequest) (ChoreographyEventPage, error) {
	if request.ChoreographyRunBinding != c.binding || request.Node == returnMissingIdentity ||
		request.MaxEvents < returnMinimumEventPageSize || request.MaxEvents > returnMaximumEventPageSize {
		return ChoreographyEventPage{}, fmt.Errorf(returnEventRequestInvalidError)
	}
	document, err := json.Marshal(request)
	if err != nil {
		return ChoreographyEventPage{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+returnEventsPath, bytes.NewReader(document))
	if err != nil {
		return ChoreographyEventPage{}, err
	}
	httpRequest.Header.Set(returnSignatureHeader, c.signer.Sign(returnEventsKind, document))
	response, err := c.client.Do(httpRequest)
	if err != nil {
		return ChoreographyEventPage{}, fmt.Errorf(returnEventResponseUnknownFormat, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ChoreographyEventPage{}, fmt.Errorf(returnEventFeedStatusFormat, response.StatusCode)
	}
	reply, err := io.ReadAll(io.LimitReader(response.Body, returnEventPageLimit+returnReplyReadOverflowBytes))
	if err != nil || len(reply) > returnEventPageLimit ||
		!c.signer.IsSignatureValid(returnPageKind, reply, response.Header.Get(returnSignatureHeader)) {
		return ChoreographyEventPage{}, fmt.Errorf(returnEventPageSignatureError)
	}
	var page ChoreographyEventPage
	if err := strictJSON(reply, &page); err != nil || len(page.Events) > request.MaxEvents ||
		page.Cursor.Sequence != request.Cursor.Sequence+uint64(len(page.Events)) ||
		(page.More && len(page.Events) == returnNoEvents) || page.Cursor.OID == returnMissingIdentity ||
		page.Head.OID == returnMissingIdentity || page.Head.Sequence < page.Cursor.Sequence ||
		page.More != (page.Head.Sequence > page.Cursor.Sequence) ||
		(len(page.Events) == returnNoEvents && request.Cursor.OID != returnMissingIdentity && page.Cursor.OID != request.Cursor.OID) {
		return ChoreographyEventPage{}, fmt.Errorf(returnEventPageContiguityError)
	}
	for index, event := range page.Events {
		if event.Sequence != request.Cursor.Sequence+uint64(index)+returnEventNextSequence ||
			!IsChoreographyEventKind(event.Kind) || event.Task == returnMissingIdentity || event.TaskOID == returnMissingIdentity {
			return ChoreographyEventPage{}, fmt.Errorf(returnEventPageStateError)
		}
	}
	return page, nil
}

var errReceiptNotFound = errors.New(returnReceiptNotFoundError)

func (c *ChoreographyReturnClient) send(ctx context.Context, input returnRequest) (ChoreographyReceipt, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+input.path, input.body)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if input.contentLength == nil {
		req.Header.Set(returnSignatureHeader, c.signer.Sign(input.kind, input.document))
	} else {
		req.Header.Set(returnDocumentHeader, base64.RawURLEncoding.EncodeToString(input.document))
		req.Header.Set(returnSignatureHeader, c.signer.Sign(input.kind, input.document))
		req.ContentLength = *input.contentLength
	}
	res, err := c.client.Do(req)
	if err != nil {
		return ChoreographyReceipt{}, fmt.Errorf(returnResponseUnknownFormat, err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound && input.kind == returnQueryKind {
		return ChoreographyReceipt{}, errReceiptNotFound
	}
	if res.StatusCode != http.StatusOK {
		return ChoreographyReceipt{}, fmt.Errorf(returnResponseUnsettledFormat, res.StatusCode)
	}
	reply, err := io.ReadAll(io.LimitReader(res.Body, returnReplyLimit+returnReplyReadOverflowBytes))
	if err != nil || len(reply) > returnReplyLimit ||
		!c.signer.IsSignatureValid(returnReplyKind, reply, res.Header.Get(returnSignatureHeader)) {
		return ChoreographyReceipt{}, fmt.Errorf(returnReceiptSignatureError)
	}
	var receipt ChoreographyReceipt
	if err := strictJSON(reply, &receipt); err != nil || receipt.ChoreographyRunBinding != c.binding ||
		receipt.ProposalID == returnMissingIdentity || receipt.OID == returnMissingIdentity ||
		(!receipt.Accepted && !receipt.Refused) || (receipt.Accepted && receipt.Refused) ||
		(receipt.Refused && (input.kind != returnClaimKind && input.kind != returnQueryKind ||
			receipt.FenceOID == returnMissingIdentity || receipt.FenceSequence == returnNoFenceSequence || receipt.RefusalReason == returnMissingIdentity)) {
		return ChoreographyReceipt{}, fmt.Errorf(returnReceiptRunMismatchError)
	}
	return receipt, nil
}

type contextReader struct {
	ctx  context.Context
	from io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return returnReadNoBytes, err
	}
	return r.from.Read(p)
}
