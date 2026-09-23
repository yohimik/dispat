package plan

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// Release tags carry the provider tags visible when their consumer published.
// Ancestry alone cannot order two releases tagged at the same source commit.
// The receipt lives in the ordinary immutable release tag, alongside its
// version, and therefore survives a filtered or interrupted later run.
const (
	receiptMarker = " dispat-seen-v1:"
	// ProviderReceiptEnvVar carries the outer run's provider observations into
	// a nested `dispat commit --tag` publish step. The orchestrator supplies
	// it after provider outcomes are known.
	ProviderReceiptEnvVar = "DISPAT_PROVIDER_RECEIPT"
)

// RenderReleaseTagMessage renders the tag's human-readable subject and its compact
// provider receipt. A sorted JSON map is encoded so names cannot inject tag
// syntax, and Go's JSON encoder gives identical inputs identical bytes.
func RenderReleaseTagMessage(tag string, providers map[string]string) string {
	return "release " + tag + receiptMarker + EncodeProviderReceipt(providers)
}

func EncodeProviderReceipt(providers map[string]string) string {
	if providers == nil {
		providers = map[string]string{}
	}
	encoded, _ := json.Marshal(providers)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func parseReleaseReceipt(tag gitx.Tag) (map[string]string, error) {
	if !tag.Annotated {
		return nil, nil
	}
	prefix := "release " + tag.Name + receiptMarker
	if !strings.HasPrefix(tag.Subject, prefix) {
		if strings.Contains(tag.Subject, " dispat-seen-") {
			return nil, fmt.Errorf("release tag %s has an unrecognized provider receipt", tag.Name)
		}
		return nil, nil // a release written before receipts were introduced
	}
	payload := strings.TrimPrefix(tag.Subject, prefix)
	providers, err := DecodeProviderReceipt(payload)
	if err != nil {
		return nil, fmt.Errorf("release tag %s: %w", tag.Name, err)
	}
	return providers, nil
}

// DecodeProviderReceipt refuses malformed and noncanonical evidence, including
// duplicate map keys. Silent normalization could change what a release saw.
func DecodeProviderReceipt(payload string) (map[string]string, error) {
	if len(payload) > 1<<20 || payload == "" {
		return nil, fmt.Errorf("invalid provider receipt length")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("invalid provider receipt: %w", err)
	}
	var providers map[string]string
	if err := json.Unmarshal(encoded, &providers); err != nil || providers == nil {
		return nil, fmt.Errorf("invalid provider receipt")
	}
	canonical, _ := json.Marshal(providers)
	if string(canonical) != string(encoded) {
		return nil, fmt.Errorf("noncanonical provider receipt")
	}
	return providers, nil
}

// receiptBoundary is the provider's last release when this consumer shipped.
// A missing provider tag is allowed only for a provider that had not yet
// released; a named tag that disappeared is damaged history, not an empty
// boundary that may be silently planned around.
func (cp *computation) resolveReceiptBoundary(consumer, provider, seenTag string) (gitx.Tag, error) {
	if seenTag == "" {
		return gitx.Tag{}, nil
	}
	if cp.receiptTags == nil {
		cp.receiptTags = make(map[string]map[string]gitx.Tag)
	}
	byName, indexed := cp.receiptTags[provider]
	if !indexed {
		byName = make(map[string]gitx.Tag, len(cp.tags[provider]))
		for _, tag := range cp.tags[provider] {
			if tag.Parsed {
				byName[tag.Name] = tag
			}
		}
		cp.receiptTags[provider] = byName
	}
	tag, found := byName[seenTag]
	if !found {
		return gitx.Tag{}, fmt.Errorf("release tag for %s records missing provider tag %s", consumer, seenTag)
	}
	if cp.seenCommits == nil {
		cp.seenCommits = make(map[string]map[string]string)
	}
	if cp.seenCommits[consumer] == nil {
		cp.seenCommits[consumer] = make(map[string]string)
	}
	cp.seenCommits[consumer][provider] = cp.tagCommitKey(provider, tag.Commit)
	return tag, nil
}

func (cp *computation) loadReceipt(pkg string, baseline gitx.Tag) error {
	providers, err := parseReleaseReceipt(baseline)
	if err != nil {
		return err
	}
	if providers == nil {
		return nil
	}
	if cp.seenProviders == nil {
		cp.seenProviders = make(map[string]map[string]string)
	}
	cp.seenProviders[pkg] = providers
	return nil
}

func (cp *computation) needsReceiptHistory(provider, seenTag string) bool {
	current, found := cp.tags[provider].Baseline()
	return found && current.Parsed && current.Name != seenTag
}

func (cp *computation) hasReceiptProvider(provider string) bool {
	return cp.byName[provider] != nil
}

// observedProviders is the planner's tag snapshot. Standalone and nested
// `dispat commit --tag` commands use it; the release executor refreshes it
// at actual publication, after same-run providers have completed.
func (cp *computation) observedProviders(rel *Release) map[string]string {
	seen := make(map[string]string)
	for _, source := range rel.Sources {
		if _, exists := seen[source.Provider]; exists {
			continue
		}
		seen[source.Provider] = ""
		if tag, ok := cp.tags[source.Provider].Baseline(); ok && tag.Parsed {
			seen[source.Provider] = tag.Name
		}
	}
	return seen
}

type providerReceipt struct {
	consumer, provider, tag string
}

func (cp *computation) listProviderReceipts() []providerReceipt {
	var receipts []providerReceipt
	for consumer, providers := range cp.seenProviders {
		for provider, tag := range providers {
			receipts = append(receipts, providerReceipt{consumer: consumer, provider: provider, tag: tag})
		}
	}
	sort.Slice(receipts, func(i, j int) bool {
		if receipts[i].consumer != receipts[j].consumer {
			return receipts[i].consumer < receipts[j].consumer
		}
		return receipts[i].provider < receipts[j].provider
	})
	return receipts
}
