// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"container/list"
	"context"
	"strings"
	"sync"
)

const (
	mailboxCacheMaximumEntries       = 256
	mailboxDocumentCacheMaximumBytes = 1 << 20
	mailboxTreeCacheMaximumBytes     = 256 << 10
	mailboxCacheBaseMetadataBytes    = 1024
	mailboxCacheEntryMetadataBytes   = 1024
	mailboxCacheAllocationFactor     = 2
	mailboxSHA1OIDHexLength          = 40
	mailboxSHA256OIDHexLength        = 64
	mailboxCacheEmptyBytes           = 0
	mailboxCacheMissingIdentityText  = ""
	mailboxCacheHitState             = true
	mailboxCacheMissState            = false
)

type mailboxDocumentKey struct {
	kind      MessageKind
	document  string
	signature string
}

type mailboxSignedDocument struct {
	document  []byte
	signature string
}

type mailboxCacheEntry[K comparable, V any] struct {
	key   K
	value V
	bytes int
}

// Only immutable, privately copied values enter this cache. The byte charge
// allows for allocation rounding and map/list metadata as well as payloads.
type mailboxObjectCache[K comparable, V any] struct {
	mu         sync.Mutex
	entries    map[K]*list.Element
	order      list.List
	bytes      int
	generation uint64
}

func (c *mailboxObjectCache[K, V]) find(ctx context.Context, key K) (V, uint64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var absent V
	if err := ctx.Err(); err != nil {
		return absent, c.generation, mailboxCacheMissState, err
	}
	entry := c.entries[key]
	if entry == nil {
		return absent, c.generation, mailboxCacheMissState, nil
	}
	c.order.MoveToFront(entry)
	return entry.Value.(mailboxCacheEntry[K, V]).value, c.generation, mailboxCacheHitState, nil
}

func (c *mailboxObjectCache[K, V]) remember(key K, value V, charge, maximumBytes int, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation || charge > maximumBytes-mailboxCacheBaseMetadataBytes {
		return
	}
	if c.entries[key] != nil {
		return
	}
	for len(c.entries) >= mailboxCacheMaximumEntries ||
		c.bytes+charge > maximumBytes-mailboxCacheBaseMetadataBytes {
		oldest := c.order.Back()
		entry := oldest.Value.(mailboxCacheEntry[K, V])
		delete(c.entries, entry.key)
		c.bytes -= entry.bytes
		c.order.Remove(oldest)
	}
	if c.entries == nil {
		c.entries = make(map[K]*list.Element)
	}
	c.entries[key] = c.order.PushFront(mailboxCacheEntry[K, V]{key: key, value: value, bytes: charge})
	c.bytes += charge
}

func (c *mailboxObjectCache[K, V]) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	c.order.Init()
	c.bytes = mailboxCacheEmptyBytes
	c.generation++
}

func mailboxCacheCharge(payloadBytes int) int {
	return mailboxCacheEntryMetadataBytes + mailboxCacheAllocationFactor*payloadBytes
}

func isMailboxImmutableOID(oid string) bool {
	return validSlotHex(oid, mailboxSHA1OIDHexLength) || validSlotHex(oid, mailboxSHA256OIDHexLength)
}

func (m *GitMailbox) rememberDocument(key mailboxDocumentKey, document []byte, signature string, generation uint64) {
	if !isMailboxImmutableOID(key.document) || !isMailboxImmutableOID(key.signature) {
		return
	}
	payload := len(key.kind) + len(key.document) + len(key.signature) + len(document) + len(signature)
	charge := mailboxCacheCharge(payload)
	if charge > mailboxDocumentCacheMaximumBytes-mailboxCacheBaseMetadataBytes {
		return
	}
	key = mailboxDocumentKey{kind: MessageKind(strings.Clone(string(key.kind))),
		document: strings.Clone(key.document), signature: strings.Clone(key.signature)}
	m.documents.remember(key, mailboxSignedDocument{
		document: bytes.Clone(document), signature: strings.Clone(signature),
	}, charge, mailboxDocumentCacheMaximumBytes, generation)
}

func (m *GitMailbox) rememberTree(oid string, tree transportTree, generation uint64) {
	if !isMailboxImmutableOID(oid) || tree.kind == mailboxCacheMissingIdentityText ||
		tree.document == mailboxCacheMissingIdentityText || tree.signature == mailboxCacheMissingIdentityText {
		return
	}
	charge := mailboxCacheCharge(len(oid) + len(tree.kind) + len(tree.document) + len(tree.signature))
	if charge > mailboxTreeCacheMaximumBytes-mailboxCacheBaseMetadataBytes {
		return
	}
	tree.kind = MessageKind(strings.Clone(string(tree.kind)))
	tree.document, tree.signature = strings.Clone(tree.document), strings.Clone(tree.signature)
	m.trees.remember(strings.Clone(oid), tree, charge, mailboxTreeCacheMaximumBytes, generation)
}
