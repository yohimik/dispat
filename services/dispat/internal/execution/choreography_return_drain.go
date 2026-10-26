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
	returnDrainInvalid               = "invalid signed drain request"
	returnDrainQueryInvalid          = "invalid signed drain query"
	returnDrainUnsettled             = "drain outcome unsettled"
	returnDrainAbsent                = "drain receipt not settled"
	returnDrainReplyInvalid          = "invalid settled drain receipt"
	returnDrainReplyEncode           = "drain receipt encoding failed"
	returnDrainResponseUnknownFormat = "execution: drain response unknown: %w"
	returnDrainResponseStatusFormat  = "execution: drain response unsettled (HTTP %d)"
	returnDrainResponseInvalid       = "execution: drain receipt signature, identity or size is invalid"
	returnDrainMissingIdentity       = ""
	returnDrainContentTypeHeader     = "Content-Type"
	returnDrainJSONMediaType         = "application/json"
	returnDrainReadOverflowBytes     = 1
	returnDrainReceiptMissing        = false
)

func (h *choreographyReturnHandler) drain(w http.ResponseWriter, r *http.Request) {
	broker, ok := h.opts.Broker.(ChoreographyDrainBrokerx)
	var proposal ChoreographyDrainProposal
	if !ok || h.readSignedJSON(choreographySignedJSONRead{Request: r, Kind: choreographyDrainKind, MaxBytes: h.opts.MaxClaimBytes, Out: &proposal}) != nil ||
		proposal.validate() != nil || proposal.ChoreographyRunBinding != h.opts.Binding {
		http.Error(w, returnDrainInvalid, http.StatusBadRequest)
		return
	}
	receipt, err := broker.DrainNode(r.Context(), proposal)
	h.writeDrainReceipt(w, receipt, err)
}

func (h *choreographyReturnHandler) drainReceipt(w http.ResponseWriter, r *http.Request) {
	broker, ok := h.opts.Broker.(ChoreographyDrainBrokerx)
	var query ChoreographyDrainReceiptQuery
	if !ok || h.readSignedJSON(choreographySignedJSONRead{Request: r, Kind: choreographyDrainQueryKind, MaxBytes: h.opts.MaxClaimBytes, Out: &query}) != nil ||
		query.ChoreographyRunBinding != h.opts.Binding || query.GraphRootOID == returnDrainMissingIdentity ||
		query.Node == returnDrainMissingIdentity || query.RequestID == returnDrainMissingIdentity || query.RequestDigest == returnDrainMissingIdentity ||
		query.ExpectedLedgerOID == returnDrainMissingIdentity {
		http.Error(w, returnDrainQueryInvalid, http.StatusBadRequest)
		return
	}
	receipt, found, err := broker.LookupDrainReceipt(r.Context(), query)
	if err != nil {
		http.Error(w, returnDrainUnsettled, http.StatusServiceUnavailable)
		return
	}
	if !found {
		http.Error(w, returnDrainAbsent, http.StatusNotFound)
		return
	}
	h.writeDrainReceipt(w, receipt, nil)
}

func (h *choreographyReturnHandler) writeDrainReceipt(w http.ResponseWriter,
	receipt ChoreographyDrainReceipt, err error) {
	if err != nil {
		http.Error(w, returnDrainUnsettled, http.StatusServiceUnavailable)
		return
	}
	if receipt.ChoreographyRunBinding != h.opts.Binding || receipt.GraphRootOID == returnDrainMissingIdentity ||
		receipt.Node == returnDrainMissingIdentity || receipt.RequestID == returnDrainMissingIdentity || receipt.RequestDigest == returnDrainMissingIdentity ||
		receipt.Cursor.OID == returnDrainMissingIdentity || receipt.Accepted == receipt.Refused {
		http.Error(w, returnDrainReplyInvalid, http.StatusInternalServerError)
		return
	}
	document, err := json.Marshal(receipt)
	if err != nil {
		http.Error(w, returnDrainReplyEncode, http.StatusInternalServerError)
		return
	}
	w.Header().Set(returnDrainContentTypeHeader, returnDrainJSONMediaType)
	w.Header().Set(returnSignatureHeader, h.opts.Signer.Sign(choreographyDrainReplyKind, document))
	_, _ = w.Write(document)
}

type choreographyDrainSendOptions struct {
	Path     string
	Kind     MessageKind
	Document []byte
	Expected ChoreographyDrainReceiptQuery
}

func (c *ChoreographyReturnClient) sendDrain(ctx context.Context,
	options choreographyDrainSendOptions) (ChoreographyDrainReceipt, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+options.Path, bytes.NewReader(options.Document))
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	request.Header.Set(returnSignatureHeader, c.signer.Sign(options.Kind, options.Document))
	response, err := c.client.Do(request)
	if err != nil {
		return ChoreographyDrainReceipt{}, fmt.Errorf(returnDrainResponseUnknownFormat, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound && options.Kind == choreographyDrainQueryKind {
		return ChoreographyDrainReceipt{}, errReceiptNotFound
	}
	if response.StatusCode != http.StatusOK {
		return ChoreographyDrainReceipt{}, fmt.Errorf(returnDrainResponseStatusFormat, response.StatusCode)
	}
	reply, err := io.ReadAll(io.LimitReader(response.Body, returnReplyLimit+returnDrainReadOverflowBytes))
	if err != nil || len(reply) > returnReplyLimit ||
		!c.signer.IsSignatureValid(choreographyDrainReplyKind, reply, response.Header.Get(returnSignatureHeader)) {
		return ChoreographyDrainReceipt{}, fmt.Errorf(returnDrainResponseInvalid)
	}
	var receipt ChoreographyDrainReceipt
	if strictJSON(reply, &receipt) != nil || receipt.ChoreographyRunBinding != c.binding ||
		receipt.BootstrapOID != options.Expected.BootstrapOID || receipt.GraphRootOID != options.Expected.GraphRootOID ||
		receipt.Node != options.Expected.Node || receipt.RequestID != options.Expected.RequestID ||
		receipt.RequestDigest != options.Expected.RequestDigest || receipt.Cursor.OID == returnDrainMissingIdentity ||
		receipt.Accepted == receipt.Refused {
		return ChoreographyDrainReceipt{}, fmt.Errorf(returnDrainResponseInvalid)
	}
	return receipt, nil
}

func (c *ChoreographyReturnClient) SubmitDrain(ctx context.Context,
	proposal ChoreographyDrainProposal) (ChoreographyDrainReceipt, error) {
	if err := proposal.validate(); err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	query, err := FormatChoreographyDrainReceiptQuery(proposal)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	document, err := json.Marshal(proposal)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	return c.sendDrain(ctx, choreographyDrainSendOptions{Path: returnDrainPath,
		Kind: choreographyDrainKind, Document: document, Expected: query})
}

func (c *ChoreographyReturnClient) LookupDrain(ctx context.Context,
	query ChoreographyDrainReceiptQuery) (ChoreographyDrainReceipt, bool, error) {
	document, err := json.Marshal(query)
	if err != nil {
		return ChoreographyDrainReceipt{}, returnDrainReceiptMissing, err
	}
	receipt, err := c.sendDrain(ctx, choreographyDrainSendOptions{Path: returnDrainReceiptPath,
		Kind: choreographyDrainQueryKind, Document: document, Expected: query})
	if err == errReceiptNotFound {
		return ChoreographyDrainReceipt{}, returnDrainReceiptMissing, nil
	}
	return receipt, err == nil, err
}
