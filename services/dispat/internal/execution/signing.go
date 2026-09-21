// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Who wrote a message, proven rather than assumed.
//
// A mailbox is a Git repository several machines can write to, so the
// transport proves nothing about authorship: the specification requires the
// serving node to authenticate the assigning orchestrator and the integrity
// of what it was sent, and a matching plan digest is explicitly not proof of
// authority (§28.3). The proof here is one HMAC-SHA256 over the exact bytes
// of the message document, with a secret both sides read from their own
// environment and neither writes down.
//
// Over the exact bytes, and never over a re-serialization: a signature that
// covered a canonical form of the document would be a signature over what the
// reader's own parser made of it, and the two differ precisely where an
// attacker would want them to.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ErrNoSecret is a signer asked to exist without a secret. It is refused at
// construction rather than at the first message, because a node that cannot
// sign cannot take part at all, and "started, then rejected everything" is a
// much worse way to find that out than "did not start".
var ErrNoSecret = errors.New("execution: the signing secret is empty")

// Signer signs and verifies mailbox messages with one shared secret.
//
// It is a type rather than two functions taking a key so that the secret is
// read once, at the one place that is allowed to read it, and every later
// caller holds a capability instead of a copy of the value. Nothing here ever
// prints, logs or returns the secret.
//
// Every signature covers the kind of message as well as its bytes. It has to:
// which message a document is depends on the name of the file it was found
// in, so a signature over the bytes alone would say that somebody knowing the
// secret wrote this text, and nothing about what they wrote it as. Anybody
// who can push to the mailbox could then take an authentic document and
// publish its exact bytes under another name, turning a withdrawal into an
// authorization or a claim into a result, without ever holding the secret.
// Binding the kind into the code is what makes a message mean one thing.
type Signer struct {
	secret []byte
}

// NewSigner builds a signer from the value of the variable `execution.secretEnv`
// names, refusing an empty one.
func NewSigner(secret string) (*Signer, error) {
	if secret == "" {
		return nil, ErrNoSecret
	}
	return &Signer{secret: []byte(secret)}, nil
}

// Sign is the lowercase hex HMAC-SHA256 over this kind of message and
// exactly these bytes, which is what travels in `dispat/<kind>.sig` beside
// the document it covers.
func (s *Signer) Sign(kind MessageKind, document []byte) string {
	return hex.EncodeToString(s.calculate(kind, document))
}

// calculate is the raw code, kept apart from its hex spelling so that
// verification compares codes rather than the strings they were written as.
//
// The kind is written first, then one NUL byte, then the document. The
// separator is what makes the pair unambiguous: a kind holds no NUL, so no
// kind and document can run together into the same bytes as another pair.
// An empty kind cannot reach here: every writer passes one of the constants,
// and a reader refuses a tip carrying no message before it verifies anything.
func (s *Signer) calculate(kind MessageKind, document []byte) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write(document)
	return mac.Sum(nil)
}

// IsSignatureValid reports whether signature is this secret's signature over
// exactly these bytes read as this kind of message.
//
// The kind is the one the reader found the document under, so a document
// lifted out of one message and published as another fails here however
// authentic its bytes are. The comparison is on the decoded bytes with
// hmac.Equal, so it takes the same time whatever the difference is, and a
// signature that is not hex at all is simply invalid rather than an error of
// its own: to a reader deciding whether to act on a message, "malformed" and
// "wrong" are one answer.
func (s *Signer) IsSignatureValid(kind MessageKind, document []byte, signature string) bool {
	offered, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(offered, s.calculate(kind, document))
}
