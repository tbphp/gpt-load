# Canonical Chromium candidate inspection

- Source SHA: `43c05ceaf1709212f0930e05f0273408f7996d3f`
- Runner: pinned Linux arm64 Chromium, as recorded in `candidate.json`
- Inspection: original-resolution image review plus manifest geometry
- Result: `PASS`
- Human baseline approval: `NOT RUN`
- Baseline activation: disabled

Every screenshot has zero body-level horizontal overflow, starts at document origin, keeps the
primary heading inside the viewport, and matches the SHA-256 recorded in `candidate.json`.

| Scenario | Result | Inspection note |
|---|---|---|
| `home-normal-desktop-en-light` | PASS | Hierarchy, focus outline, status, cards, long selector, and code block remain visible. |
| `home-normal-mobile-zh-dark` | PASS | Mobile stack, long identifier wrapping, status semantics, code wrapping, and actions remain contained. |
| `home-anomaly-tablet-en-dark` | PASS | Stale, warning, failure, and serviceability signals remain contained and readable. |
| `home-empty-error-tablet-zh-light` | PASS | Health/usage failures, Group empty state, and no-AccessKey connection state remain distinct. |
| `access-keys-long-desktop-en-light` | PASS | Long name/model wrapping does not change row meaning or body geometry. |
| `access-keys-long-mobile-zh-dark` | PASS | The status badge stays on one line while the long name wraps without overlap. |
| `access-key-operation-tablet-en-light` | PASS | The unknown-outcome notice and sole reconciliation action remain visible. |
| `model-prices-tablet-zh-dark` | PASS | Built-in/override sections, policy copy, explicit zero, and unset values remain distinguishable. |
| `settings-dirty-desktop-en-light` | PASS | Page origin, dirty summary, owner-level actions, focused value, and all sections render in order. |
| `settings-validation-mobile-zh-light` | PASS | Page origin, validation summary, disabled save, field error, and system metadata remain contained. |
| `usage-quality-desktop-en-dark` | PASS | Quality, pipeline, freshness, trend, and breakdown hierarchy remain readable. |
| `usage-quality-tablet-zh-light` | PASS | Tablet cards and quality warnings remain ordered; wide data stays in its local scroll surface. |
| `logs-signal-tablet-en-light` | PASS | Applied filters, freshness, and the request signal path remain visible in the local table surface. |
| `logs-signal-mobile-zh-dark` | PASS | Filter controls stack correctly; the wide log table stays inside its local scroll surface. |
| `inspector-routing-desktop-en-light` | PASS | Input, observation, candidate Group, weights, and status hierarchy remain visible. |
| `inspector-routing-tablet-zh-dark` | PASS | Two-column tablet layout, long identifiers, and candidate/key status remain contained. |

Two defects found in earlier candidates were fixed before this candidate was generated:

1. full-page screenshots could retain interaction scroll and relocate sticky UI in the artifact;
2. the mobile AccessKey status badge could collapse into vertical Chinese text, while a first
   global fix made long Home status text overflow.

The final implementation resets capture scroll deterministically and limits non-shrinking,
single-line badge behavior to AccessKey mobile cards. No candidate was activated automatically.
