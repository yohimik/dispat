// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Preflight replies use the same direct HTTPS return channel. The peer never
// pushes its probe Claim or Result; the first node validates the exact signed
// assignment and persists both steps before admitting its NodeReport.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"time"
)

const choreographyUnknownWorkerPreflightErrorMessage = "execution: preflight reply names an unknown worker"

// probeProtocol binds the preflight capability to the run's selected
// transport. Legacy v2 probes remain v2; a v3 run refuses v2 peers before
// any task branch can be activated.
func (c *Coordinator) probeProtocol() int {
	if c != nil && c.Mode != "" {
		return ChoreographyProtocolVersion
	}
	return ProtocolVersion
}

type ProbeHandlerOptions struct {
	Coordinator *Coordinator
	Signer      *Signer
	Binding     ChoreographyRunBinding
	MaxBytes    int64
}

type choreographyProbeHandler struct{ opts ProbeHandlerOptions }

func NewChoreographyProbeHandler(opts ProbeHandlerOptions) (http.Handler, error) {
	if opts.Coordinator == nil || opts.Signer == nil || !opts.Binding.valid() || opts.MaxBytes <= 0 {
		return nil, fmt.Errorf("execution: incomplete choreography preflight handler")
	}
	return &choreographyProbeHandler{opts: opts}, nil
}

func (h *choreographyProbeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil {
		http.Error(w, "TLS required", http.StatusUpgradeRequired)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != returnProbePath {
		http.NotFound(w, r)
		return
	}
	var reply ChoreographyProbeReply
	proxy := choreographyReturnHandler{opts: ReturnHandlerOptions{Signer: h.opts.Signer}}
	if err := proxy.readSignedJSON(choreographySignedJSONRead{Request: r, Kind: returnProbeKind, MaxBytes: h.opts.MaxBytes, Out: &reply}); err != nil ||
		reply.ChoreographyRunBinding != h.opts.Binding || reply.Node == "" || reply.Branch == "" || reply.AssignmentOID == "" {
		http.Error(w, "invalid preflight reply", http.StatusBadRequest)
		return
	}
	receipt, err := h.opts.Coordinator.AcceptProbeReply(r.Context(), reply)
	proxy.opts.Binding = h.opts.Binding
	proxy.writeReceipt(w, receipt, err)
}

func (c *Coordinator) AcceptProbeReply(ctx context.Context, reply ChoreographyProbeReply) (ChoreographyReceipt, error) {
	c.probeReplies.Lock()
	defer c.probeReplies.Unlock()
	if reply.Run != c.Run || reply.Generation != c.Generation || reply.PlanDigest != c.PlanDigest ||
		reply.GraphDigest != c.GraphDigest || reply.Report.Protocol != c.probeProtocol() {
		return ChoreographyReceipt{}, fmt.Errorf("execution: preflight reply belongs to another run")
	}
	if err := c.checkOwnership(ctx); err != nil {
		return ChoreographyReceipt{}, err
	}
	if err := c.checkReport(&reply.Report); err != nil {
		return ChoreographyReceipt{}, err
	}
	m := c.mailboxes[reply.Node]
	if m == nil {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyUnknownWorkerPreflightErrorMessage)
	}
	owner := c.ownerNode(reply.Node, reply.Branch)
	c.mu.Lock()
	owned := false
	for _, lease := range c.owned[owner] {
		if lease.Branch == reply.Branch && lease.ExpectedOld != "" {
			owned = true
			break
		}
	}
	c.mu.Unlock()
	if !owned {
		return ChoreographyReceipt{}, fmt.Errorf("execution: preflight reply names no owned probe ref")
	}
	head, err := m.Reread(ctx, reply.Branch)
	if err != nil || head.OID == "" {
		return ChoreographyReceipt{}, fmt.Errorf("execution: preflight ref cannot be read: %w", err)
	}
	tip, err := m.Inspect(ctx, head)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if tip.Kind == MessageResult {
		if tip.Previous != MessageClaim {
			return ChoreographyReceipt{}, fmt.Errorf("execution: preflight result has no claim")
		}
		doc, err := m.Read(ctx, tip, c.Limits.MaxManifestBytes)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		var result Result
		if err := strictJSON(doc, &result); err != nil || c.checkResult(result, Link{Name: reply.Node}, tip, reply.AssignmentOID) != "" ||
			!reflect.DeepEqual(result.Report, &reply.Report) {
			return ChoreographyReceipt{}, fmt.Errorf("execution: preflight result differs from reply")
		}
		return c.probeReceipt(reply, tip.OID), nil
	}
	var claimed string
	if tip.Kind == MessageClaim {
		if tip.PreviousOID != reply.AssignmentOID {
			return ChoreographyReceipt{}, fmt.Errorf("execution: preflight claim answers another assignment")
		}
		doc, err := m.Read(ctx, tip, c.Limits.MaxManifestBytes)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		var claim Claim
		if err := strictJSON(doc, &claim); err != nil || claim.Assignment != reply.AssignmentOID || claim.Node != reply.Node ||
			claim.Run != c.Run || claim.PlanDigest != c.PlanDigest || claim.Generation != c.Generation ||
			claim.Task != PreflightTask || claim.Attempt != 1 || claim.Branch != reply.Branch || claim.Kind != KindProbe {
			return ChoreographyReceipt{}, fmt.Errorf("execution: preflight claim differs from reply")
		}
		claimed = tip.OID
	} else if tip.Kind == MessageAssignment && tip.OID == reply.AssignmentOID {
		doc, err := m.Read(ctx, tip, c.Limits.MaxManifestBytes)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		var assignment Assignment
		if err := strictJSON(doc, &assignment); err != nil || assignment.Kind != KindProbe ||
			assignment.Run != c.Run || assignment.Generation != c.Generation ||
			assignment.Node != reply.Node || assignment.Branch != reply.Branch ||
			assignment.ReturnEndpoint != c.ReturnEndpoint || assignment.ReturnCertSHA256 != c.ReturnCertSHA256 ||
			assignment.GraphDigest != c.GraphDigest {
			return ChoreographyReceipt{}, fmt.Errorf("execution: preflight assignment differs from signed run")
		}
		claim := Claim{Header: c.probeHeader(reply), Assignment: reply.AssignmentOID}
		data, err := json.Marshal(claim)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		claimed, err = m.Advance(ctx, reply.Branch, reply.AssignmentOID, MessageClaim, data, nil)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		c.recordOwnedRef(ctx, ownedRefStep{node: reply.Node, branch: reply.Branch, oid: claimed, parent: reply.AssignmentOID})
	} else {
		return ChoreographyReceipt{}, fmt.Errorf("execution: preflight ref is not the offered assignment")
	}
	result := Result{Header: c.probeHeader(reply), Assignment: reply.AssignmentOID,
		Status: StatusSucceeded, Platform: Platform{OS: reply.Report.OS, Arch: reply.Report.Arch, Dispat: reply.Report.Dispat},
		Report: &reply.Report}
	data, err := json.Marshal(result)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	resultOID, err := m.Advance(ctx, reply.Branch, claimed, MessageResult, data, nil)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	c.recordOwnedRef(ctx, ownedRefStep{node: reply.Node, branch: reply.Branch, oid: resultOID, parent: claimed})
	return c.probeReceipt(reply, resultOID), nil
}

func (c *Coordinator) probeHeader(reply ChoreographyProbeReply) Header {
	return Header{Protocol: c.probeProtocol(), Kind: KindProbe, Run: c.Run, PlanDigest: c.PlanDigest,
		Task: PreflightTask, Attempt: 1, Generation: c.Generation, Node: reply.Node, Branch: reply.Branch,
		IssuedAt: time.Now().UTC().Format(time.RFC3339)}
}

func (c *Coordinator) probeReceipt(reply ChoreographyProbeReply, oid string) ChoreographyReceipt {
	return ChoreographyReceipt{ChoreographyRunBinding: reply.ChoreographyRunBinding, Kind: MessageResult,
		Task: PreflightTask, Attempt: 1, Node: reply.Node, ProposalID: reply.AssignmentOID,
		Branch: reply.Branch, OID: oid, Accepted: true, Terminal: true}
}
