package ccme

import (
	"strings"
	"sync"
	"testing"
)

// Representative inputs, in rough order of how often each shape appears in a
// real history.
const (
	benchSubject = "feat(@acme/core): add cursor pagination to the streaming reader"

	benchSimple = benchSubject + "\n\n" +
		"The buffered reader is retained; the streaming path is opt-in via\n" +
		"createReader({ stream: true }). Cursors are opaque and must not be\n" +
		"parsed by callers.\n"

	benchDirectives = "refactor(@acme/core,@acme/cli)^^inherit@beta!: remove the v1 plugin interface\n\n" +
		"The codemod at tools/codemods/plugins-v2 handles the mechanical part.\n\n" +
		"BREAKING CHANGE: registerPlugin is gone. Use plugins: [] in the\n" +
		"config object.\n" +
		"Propagate-Channel: beta\n" +
		"Propagate-Channel-Depth: all\n" +
		"Propagate-Scope: @acme/*, -@acme/experimental-*\n" +
		"Signed-off-by: A Developer <dev@example.com>\n"

	benchMultiUnit = "feat(@acme/api): add cursor pagination\n\n---\n\n" +
		"fix(@acme/api): reject negative page sizes\n\n---\n\n" +
		"test(@acme/api): cover cursor edge cases\n\n---\n\n" +
		"docs(docs-site): document pagination\n"
)

// benchCRLF is the same message as benchSimple but needing normalisation, to
// separate the fast path from the rewrite path.
var benchCRLF = strings.ReplaceAll(benchSimple, "\n", "\r\n") + "   \r\n\r\n"

func BenchmarkParseSubject(b *testing.B) {
	p := DefaultParser()
	b.ReportAllocs()
	b.SetBytes(int64(len(benchSubject)))
	for i := 0; i < b.N; i++ {
		if _, err := p.ParseSubject(benchSubject); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseSimple(b *testing.B) {
	p := DefaultParser()
	b.ReportAllocs()
	b.SetBytes(int64(len(benchSimple)))
	for i := 0; i < b.N; i++ {
		if _, err := p.Parse(benchSimple); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseDirectives(b *testing.B) {
	p := DefaultParser()
	b.ReportAllocs()
	b.SetBytes(int64(len(benchDirectives)))
	for i := 0; i < b.N; i++ {
		if _, err := p.Parse(benchDirectives); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseMultiUnit(b *testing.B) {
	p := DefaultParser()
	b.ReportAllocs()
	b.SetBytes(int64(len(benchMultiUnit)))
	for i := 0; i < b.N; i++ {
		if _, err := p.Parse(benchMultiUnit); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseCRLF(b *testing.B) {
	p := DefaultParser()
	b.ReportAllocs()
	b.SetBytes(int64(len(benchCRLF)))
	for i := 0; i < b.N; i++ {
		if _, err := p.Parse(benchCRLF); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseInvalid measures the error path, which allocates a diagnostic
// and an aggregate error.
func BenchmarkParseInvalid(b *testing.B) {
	p := DefaultParser()
	const msg = "feat(core)^^minor+2: contradictory depth"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := p.Parse(msg); err == nil {
			b.Fatal("expected an error")
		}
	}
}

func BenchmarkNormalizeFastPath(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchSimple)))
	// benchSimple ends in a newline, so trim it to hit the no-op path.
	msg := strings.TrimRight(benchSimple, "\n")
	for i := 0; i < b.N; i++ {
		if Normalize(msg) != msg {
			b.Fatal("expected the input to be returned unchanged")
		}
	}
}

func BenchmarkNormalizeRewrite(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchCRLF)))
	for i := 0; i < b.N; i++ {
		_ = Normalize(benchCRLF)
	}
}

// BenchmarkParseHistory models the real workload: one parser sweeping many
// messages of mixed shape.
func BenchmarkParseHistory(b *testing.B) {
	p := DefaultParser()
	corpus := []string{benchSubject, benchSimple, benchDirectives, benchMultiUnit, benchCRLF}
	total := 0
	for _, m := range corpus {
		total += len(m)
	}
	b.ReportAllocs()
	b.SetBytes(int64(total))
	for i := 0; i < b.N; i++ {
		for _, m := range corpus {
			if _, err := p.Parse(m); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkParseParallel checks that a shared parser scales across goroutines,
// which it must: a Parser holds no mutable state.
func BenchmarkParseParallel(b *testing.B) {
	p := DefaultParser()
	b.ReportAllocs()
	b.SetBytes(int64(len(benchDirectives)))
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := p.Parse(benchDirectives); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestNoDataRaceOnSharedParser exercises the shared-parser guarantee under
// -race, including the configuration slices that units now alias.
func TestNoDataRaceOnSharedParser(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				res, err := p.Parse(benchDirectives)
				if err != nil {
					t.Error(err)
					return
				}
				if len(res.Units[0].Directives.Kinds) == 0 {
					t.Error("expected propagate kinds")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestSubstringsAliasTheMessage documents and pins the zero-copy contract: the
// pieces a caller reads back are windows onto the normalised message, not
// copies of it.
func TestSubstringsAliasTheMessage(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse(benchSimple)
	if err != nil {
		t.Fatal(err)
	}
	u := res.Units[0]

	within := func(name, part string) {
		t.Helper()
		if part == "" {
			return
		}
		if !strings.Contains(res.Message, part) {
			t.Errorf("%s is not a substring of the normalised message", name)
		}
	}
	within("Unit.Raw", u.Raw)
	within("Unit.Body", u.Body)
	within("Header.Raw", u.Header.Raw)
	within("Header.Description", u.Header.Description)
	for _, s := range u.Scopes() {
		within("scope term", s.Raw)
	}

	if u.Raw != res.Message {
		t.Errorf("a single-unit message should have Raw equal to the whole message")
	}
}

// TestEscapedSeparatorStillRebuilds covers the one case where a unit's text is
// not contiguous in the message and must be rebuilt.
func TestEscapedSeparatorStillRebuilds(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("docs(core): a\n\nbefore\n\n\\---\n\nafter")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Units) != 1 {
		t.Fatalf("got %d units, want 1", len(res.Units))
	}
	u := res.Units[0]
	if !strings.Contains(u.Body, "\n---\n") {
		t.Errorf("body = %q, want an unescaped --- line", u.Body)
	}
	if !strings.Contains(u.Raw, "\n---\n") {
		t.Errorf("raw = %q, want an unescaped --- line", u.Raw)
	}
	if strings.Contains(u.Body, "\\---") {
		t.Errorf("body still contains the escape: %q", u.Body)
	}
}
