// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// Cold and warm use the same current implementation and objects. Cold
// includes Forget so every operation takes the real Git path; these are not
// before/after revision measurements.
func BenchmarkMailboxImmutableRead(b *testing.B) {
	const bodyLimit = 8192
	body := `{"payload":"` + strings.Repeat("a", 4096) + `"}`
	for _, cold := range []bool{true, false} {
		name := "warm"
		if cold {
			name = "cold"
		}
		b.Run("signed-document/"+name, func(b *testing.B) {
			fixture := newMailboxFixture(b)
			tip := mailboxBlobTip(b, fixture, body)
			expected := []byte(body)
			if _, err := fixture.mailbox.Read(b.Context(), tip, bodyLimit); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			before := gitx.GitInvocations()
			for range b.N {
				if cold {
					fixture.mailbox.Forget()
				}
				actual, err := fixture.mailbox.Read(b.Context(), tip, bodyLimit)
				if err != nil || !bytes.Equal(expected, actual) {
					b.Fatalf("signed document changed: %v", err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(gitx.GitInvocations()-before)/float64(b.N), "git/op")
		})
		b.Run("tree/"+name, func(b *testing.B) {
			fixture := newMailboxFixture(b)
			oid := mailboxCacheCommit(b, fixture, body)
			expected, err := fixture.mailbox.readTree(b.Context(), oid)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			before := gitx.GitInvocations()
			for range b.N {
				if cold {
					fixture.mailbox.Forget()
				}
				actual, err := fixture.mailbox.readTree(b.Context(), oid)
				if err != nil || expected != actual {
					b.Fatalf("transport tree changed: %v", err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(gitx.GitInvocations()-before)/float64(b.N), "git/op")
		})
	}
}
