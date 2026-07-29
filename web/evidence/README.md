# Evidence binding rules

Repository evidence is a local pre-verification record. GitHub Actions is the final source of truth
for checks on a pushed commit.

- The final evidence commit in a delivery batch may change only `web/evidence/**`; it must not
  change production code or tests.
- `local-gates.json.evidence_head` identifies the parent of that evidence-only commit: the exact
  production tree that was tested. The evidence commit itself records results without changing
  production semantics.
- Every gate that claims a commit-bound result must use the same tested SHA. A dirty working tree
  cannot be represented as a commit-level PASS.
- Every audit refreshes `external-gates.json` from the real remote state. Missing runs, failed jobs,
  SHA mismatches, manual checks and unavailable environments remain `NOT RUN`, `FAIL` or
  `EXTERNAL`; historical PASS results are never carried forward as current evidence.
