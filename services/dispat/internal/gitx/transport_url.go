// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// What a node's mailbox address may be.
//
// An execution endpoint is a Git URL dispat hands to git as a remote, and
// every rule below exists because of what git would do with the value
// otherwise. A leading "-" is read as an option rather than as a remote; a
// "<transport>::<address>" value names a helper program to run; an
// unauthenticated scheme would let anyone who can reach the network write
// into a mailbox; and user information would put a credential into a
// configuration file that is committed and read by everyone who clones the
// repository.
//
// It is a sibling of requireCredentialFreeURL rather than a change to it: a
// fleet link and an execution endpoint are refused for different reasons and
// a reader of either message deserves the one that applies.

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// RequireTransportEndpoint refuses an execution endpoint dispat could not
// fetch from and push to safely. It is exported because the configuration
// loader is what states the key path a reader has to find, while what counts
// as a usable remote is Git's question and belongs here.
//
// Accepted: an https, ssh or file URL, an absolute path, or the scp-like
// [user@]host:path form without a password. Refused everywhere: user
// information, a query, a fragment, a leading "-" and a "::" helper prefix.
// The caller has already refused an empty value where the key is required;
// an empty value reaching here is not a remote and is refused as one.
func RequireTransportEndpoint(endpoint string) error {
	if strings.HasPrefix(endpoint, "-") {
		return fmt.Errorf("gitx: execution endpoint %s starts with a dash, which git reads as an option rather than a remote",
			redactEndpoint(endpoint))
	}
	if strings.Contains(endpoint, "::") {
		return fmt.Errorf("gitx: execution endpoint %s uses the transport::address form, which names a helper program to run",
			redactEndpoint(endpoint))
	}
	if strings.Contains(endpoint, "?") {
		return fmt.Errorf("gitx: execution endpoint %s carries a query, which a mailbox address has no use for",
			redactEndpoint(endpoint))
	}
	if strings.Contains(endpoint, "#") {
		return fmt.Errorf("gitx: execution endpoint %s carries a fragment, which a mailbox address has no use for",
			redactEndpoint(endpoint))
	}
	// An absolute path is answered before the scheme is read, so that a folder
	// whose name happens to hold "://" is still the path it plainly is.
	if strings.HasPrefix(endpoint, "/") {
		return nil
	}
	if scheme, _, isURL := strings.Cut(endpoint, "://"); isURL {
		return requireTransportURL(endpoint, strings.ToLower(scheme))
	}
	return requireTransportPath(endpoint)
}

// requireTransportURL checks the scheme-carrying form. The scheme is decided
// first and the value is parsed afterwards, so that an unauthenticated scheme
// is named as such instead of being reported as whatever else is wrong with
// the rest of the address.
func requireTransportURL(endpoint, scheme string) error {
	switch scheme {
	case "http", "git":
		return fmt.Errorf("gitx: execution endpoint %s uses %s, which authenticates nobody; use https, ssh or file",
			redactEndpoint(endpoint), scheme)
	case "https", "ssh", "file":
	default:
		return fmt.Errorf("gitx: execution endpoint %s uses the %s scheme; a mailbox is reached over https, ssh or file",
			redactEndpoint(endpoint), scheme)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		// The parser's own error quotes the address it failed on, which for a
		// malformed URL is exactly the text that may still hold a password, so
		// the reason travels without it.
		return errors.New("gitx: execution endpoint is not a URL git can fetch from")
	}
	if isCredentialCarried(parsed, scheme) {
		return fmt.Errorf("gitx: execution endpoint %s carries user information; a mailbox endpoint is credential free and its secret is named by secretEnv",
			redactEndpoint(endpoint))
	}
	if scheme != "file" && parsed.Host == "" {
		return fmt.Errorf("gitx: execution endpoint %s names no host", redactEndpoint(endpoint))
	}
	return nil
}

// requireTransportPath checks what is left once neither a scheme nor an
// absolute path was written: the scp-like [user@]host:path form git accepts
// for ssh. A password in the user half is refused, and so is anything that is
// not that form at all, because a relative path would resolve against
// whichever folder the node happened to start in.
// isCredentialCarried reports whether the URL's user half could be a secret.
// Over ssh a bare user name is an account rather than a credential, and it is
// the only spelling a hosted remote accepts (ssh://git@host/path), exactly as
// the scp-like form git@host:path is; a password beside it is still refused.
// Over https the user half is where a token is pasted, with or without a
// password, so anything there is refused.
func isCredentialCarried(parsed *url.URL, scheme string) bool {
	if parsed.User == nil {
		return false
	}
	if scheme != "ssh" {
		return true
	}
	_, isPasswordStated := parsed.User.Password()
	return isPasswordStated
}

func requireTransportPath(endpoint string) error {
	address := endpoint
	if userinfo, rest, hasUser := strings.Cut(endpoint, "@"); hasUser {
		if strings.Contains(userinfo, ":") {
			// The value is not written back into the message: it is the
			// password.
			return errors.New("gitx: execution endpoint carries a password; a mailbox endpoint is credential free and its secret is named by secretEnv")
		}
		address = rest
	}
	host, path, isScpForm := strings.Cut(address, ":")
	if !isScpForm || host == "" || path == "" {
		return fmt.Errorf("gitx: execution endpoint %s is not a git remote; write an https, ssh or file URL, an absolute path, or host:path",
			redactEndpoint(endpoint))
	}
	return nil
}

// redactEndpoint renders an endpoint for a message, and it is the only way
// one is written into an error here. RedactURL covers every form Go's URL
// parser recognises, but a refused endpoint is by definition one it may not
// recognise: the scp-like form is not a URL at all, and a value carrying a
// password is refused precisely because it carries one. So the user half, the
// query and the fragment are taken off whatever survived, because those are
// the three places a credential would sit.
func redactEndpoint(endpoint string) string {
	safe := RedactURL(endpoint)
	if safe == endpoint {
		if userinfo, address, hasUser := strings.Cut(endpoint, "@"); hasUser && strings.Contains(userinfo, ":") {
			safe = "REDACTED@" + address
		}
	}
	if head, _, hasQuery := strings.Cut(safe, "?"); hasQuery {
		safe = head + "?REDACTED"
	}
	if head, _, hasFragment := strings.Cut(safe, "#"); hasFragment {
		safe = head + "#REDACTED"
	}
	return safe
}
