package catalog

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"
)

// Volcengine Ark publishes prices in CNY while GPT-Load's pricing contract is
// fixed to USD. The embedded catalog uses the ECB reference rates from
// 2026-08-18 (EUR/USD 1.1576 and EUR/CNY 7.8049), or
// 1 CNY = 0.148317082858 USD, rounded to the nearest nano-USD.
//
// Price source: https://docs.volcengine.com/docs/82379/1544106?lang=zh
// Model source: https://docs.volcengine.com/docs/82379/1330310?lang=zh
// FX sources:
// https://data-api.ecb.europa.eu/service/data/EXR/D.USD.EUR.SP00.A?startPeriod=2026-08-18&endPeriod=2026-08-18&format=csvdata
// https://data-api.ecb.europa.eu/service/data/EXR/D.CNY.EUR.SP00.A?startPeriod=2026-08-18&endPeriod=2026-08-18&format=csvdata
// DeepSeek V4 Flash GA uses the announced prices effective 2026-08-21 so the
// bundled catalog does not become stale immediately after this release.

//go:embed official.json
var officialCatalogJSON []byte

var (
	officialCatalogOnce     sync.Once
	officialCatalogSnapshot *Snapshot
	officialCatalogErr      error
)

// OfficialSnapshot returns an immutable caller-owned copy of GPT-Load's
// Models.dev-compatible supplement catalog.
func OfficialSnapshot() (*Snapshot, error) {
	officialCatalogOnce.Do(func() {
		officialCatalogSnapshot, officialCatalogErr = Parse(bytes.NewReader(officialCatalogJSON))
		if officialCatalogErr != nil {
			officialCatalogErr = fmt.Errorf("parse embedded official catalog: %w", officialCatalogErr)
		}
	})
	if officialCatalogErr != nil {
		return nil, officialCatalogErr
	}
	return cloneSnapshot(officialCatalogSnapshot), nil
}

// MergeOfficial overlays complete official model records on a Models.dev
// snapshot. Models.dev providers and models that are absent from the official
// supplement remain available.
func MergeOfficial(snapshot *Snapshot) (*Snapshot, error) {
	official, err := OfficialSnapshot()
	if err != nil {
		return nil, err
	}
	return mergeSnapshots(snapshot, official), nil
}

func mergeSnapshots(base, override *Snapshot) *Snapshot {
	merged := cloneSnapshot(base)
	if merged == nil {
		merged = &Snapshot{}
	}
	if merged.Providers == nil {
		merged.Providers = make(map[string]Provider)
	}
	if override == nil {
		return merged
	}
	for providerID, overrideProvider := range override.Providers {
		provider := cloneProvider(overrideProvider)
		if provider.Models == nil {
			provider.Models = make(map[string]Model)
		}
		if existing, ok := merged.Providers[providerID]; ok {
			for modelID, model := range existing.Models {
				if _, overridden := provider.Models[modelID]; overridden {
					continue
				}
				provider.Models[modelID] = cloneModel(model)
			}
		}
		merged.Providers[providerID] = provider
	}
	return merged
}
