// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	joinRequestInvalid          = "invalid signed join request"
	joinQueryInvalid            = "invalid signed join receipt query"
	joinUnavailable             = "join outcome unsettled"
	joinReceiptUnavailable      = "join receipt not settled"
	joinReceiptInvalid          = "invalid settled join receipt"
	joinReceiptEncoding         = "join receipt encoding failed"
	joinResponseUnknownFormat   = "execution: join response unknown: %w"
	joinResponseUnsettledFormat = "execution: join response unsettled (HTTP %d)"
	joinResponseInvalid         = "execution: join receipt signature, identity or size is invalid"
	joinReturnMissingIdentity   = ""
	joinReturnContentTypeHeader = "Content-Type"
	joinReturnJSONMediaType     = "application/json"
	joinReturnReadOverflowBytes = 1
	joinReturnReceiptMissing    = false
)

func (h *choreographyReturnHandler) join(w http.ResponseWriter, r *http.Request) {
	broker, ok := h.opts.Broker.(ChoreographyJoinBrokerx)
	var proposal ChoreographyJoinProposal
	if !ok || h.readSignedJSON(choreographySignedJSONRead{Request: r, Kind: choreographyJoinKind, MaxBytes: h.opts.MaxClaimBytes, Out: &proposal}) != nil ||
		proposal.validate() != nil || proposal.ChoreographyRunBinding != h.opts.Binding {
		http.Error(w, joinRequestInvalid, http.StatusBadRequest)
		return
	}
	receipt, err := broker.AdmitNode(r.Context(), proposal)
	h.writeJoinReceipt(w, receipt, err)
}

func (h *choreographyReturnHandler) joinReceipt(w http.ResponseWriter, r *http.Request) {
	broker, ok := h.opts.Broker.(ChoreographyJoinBrokerx)
	var query ChoreographyJoinReceiptQuery
	if !ok || h.readSignedJSON(choreographySignedJSONRead{Request: r, Kind: choreographyJoinQueryKind, MaxBytes: h.opts.MaxClaimBytes, Out: &query}) != nil ||
		query.ChoreographyRunBinding != h.opts.Binding || query.GraphRootOID == joinReturnMissingIdentity ||
		query.Node == joinReturnMissingIdentity || query.RequestID == joinReturnMissingIdentity || query.RequestDigest == joinReturnMissingIdentity ||
		query.ExpectedLedgerOID == joinReturnMissingIdentity {
		http.Error(w, joinQueryInvalid, http.StatusBadRequest)
		return
	}
	receipt, found, err := broker.LookupJoinReceipt(r.Context(), query)
	if err != nil {
		http.Error(w, joinUnavailable, http.StatusServiceUnavailable)
		return
	}
	if !found {
		http.Error(w, joinReceiptUnavailable, http.StatusNotFound)
		return
	}
	h.writeJoinReceipt(w, receipt, nil)
}

func (h *choreographyReturnHandler) writeJoinReceipt(w http.ResponseWriter, receipt ChoreographyJoinReceipt, err error) {
	if err != nil {
		http.Error(w, joinUnavailable, http.StatusServiceUnavailable)
		return
	}
	if receipt.ChoreographyRunBinding != h.opts.Binding || receipt.GraphRootOID == joinReturnMissingIdentity ||
		receipt.Node == joinReturnMissingIdentity || receipt.RequestID == joinReturnMissingIdentity || receipt.RequestDigest == joinReturnMissingIdentity ||
		(receipt.Accepted == receipt.Refused) || receipt.Cursor.OID == joinReturnMissingIdentity {
		http.Error(w, joinReceiptInvalid, http.StatusInternalServerError)
		return
	}
	document, err := json.Marshal(receipt)
	if err != nil {
		http.Error(w, joinReceiptEncoding, http.StatusInternalServerError)
		return
	}
	w.Header().Set(joinReturnContentTypeHeader, joinReturnJSONMediaType)
	w.Header().Set(returnSignatureHeader, h.opts.Signer.Sign(choreographyJoinReplyMessageKind, document))
	_, _ = w.Write(document)
}

type choreographyJoinSendOptions struct {
	Path     string
	Kind     MessageKind
	Document []byte
	Expected ChoreographyJoinReceiptQuery
}

func (c *ChoreographyReturnClient) sendJoin(ctx context.Context,
	options choreographyJoinSendOptions) (ChoreographyJoinReceipt, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+options.Path, bytes.NewReader(options.Document))
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	request.Header.Set(returnSignatureHeader, c.signer.Sign(options.Kind, options.Document))
	response, err := c.client.Do(request)
	if err != nil {
		return ChoreographyJoinReceipt{}, fmt.Errorf(joinResponseUnknownFormat, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound && options.Kind == choreographyJoinQueryKind {
		return ChoreographyJoinReceipt{}, errReceiptNotFound
	}
	if response.StatusCode != http.StatusOK {
		return ChoreographyJoinReceipt{}, fmt.Errorf(joinResponseUnsettledFormat, response.StatusCode)
	}
	reply, err := io.ReadAll(io.LimitReader(response.Body, returnReplyLimit+joinReturnReadOverflowBytes))
	if err != nil || len(reply) > returnReplyLimit ||
		!c.signer.IsSignatureValid(choreographyJoinReplyMessageKind, reply, response.Header.Get(returnSignatureHeader)) {
		return ChoreographyJoinReceipt{}, fmt.Errorf(joinResponseInvalid)
	}
	var receipt ChoreographyJoinReceipt
	if strictJSON(reply, &receipt) != nil || receipt.ChoreographyRunBinding != c.binding ||
		receipt.BootstrapOID != options.Expected.BootstrapOID || receipt.GraphRootOID != options.Expected.GraphRootOID ||
		receipt.Node != options.Expected.Node || receipt.RequestID != options.Expected.RequestID ||
		receipt.RequestDigest != options.Expected.RequestDigest || receipt.Cursor.OID == joinReturnMissingIdentity ||
		(receipt.Accepted == receipt.Refused) ||
		(receipt.Accepted && (receipt.Member.Name != options.Expected.Node || receipt.Member.AuthorityID == joinReturnMissingIdentity)) {
		return ChoreographyJoinReceipt{}, fmt.Errorf(joinResponseInvalid)
	}
	return receipt, nil
}

func (c *ChoreographyReturnClient) SubmitJoin(ctx context.Context, proposal ChoreographyJoinProposal) (ChoreographyJoinReceipt, error) {
	if err := proposal.validate(); err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	query, err := FormatChoreographyJoinReceiptQuery(proposal)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	document, err := json.Marshal(proposal)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	return c.sendJoin(ctx, choreographyJoinSendOptions{Path: returnJoinPath,
		Kind: choreographyJoinKind, Document: document, Expected: query})
}

func (c *ChoreographyReturnClient) LookupJoin(ctx context.Context, query ChoreographyJoinReceiptQuery) (ChoreographyJoinReceipt, bool, error) {
	document, err := json.Marshal(query)
	if err != nil {
		return ChoreographyJoinReceipt{}, joinReturnReceiptMissing, err
	}
	receipt, err := c.sendJoin(ctx, choreographyJoinSendOptions{Path: returnJoinReceiptPath,
		Kind: choreographyJoinQueryKind, Document: document, Expected: query})
	if err == errReceiptNotFound {
		return ChoreographyJoinReceipt{}, joinReturnReceiptMissing, nil
	}
	return receipt, err == nil, err
}
