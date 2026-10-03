# POLICY_PERFORMANCE.md

Frozen performance report for the `internal/policy` pure evaluator.

- Status: **frozen** (measurement snapshot, not a rolling dashboard)
- Baseline commit: `4fd044579f17631d694ce77cdaf77b28f44f797e` (`feat(policy): freeze dynamic pricing factors and explain billing receipts`) plus the active working tree
- Benchmark file: `internal/policy/policy_benchmark_test.go` (new, this change)
- Scope: pure in-memory evaluation only. No production code or dependency changes accompany this report.
- Provenance (reconstructed from the measured tree, not invented): measured against the **active uncommitted working tree** on top of commit `4fd044579f17631d694ce77cdaf77b28f44f797e` (`feat(policy): freeze dynamic pricing factors and explain billing receipts`), plus uncommitted production changes under `internal/policy` (`eval.go`, `runtime.go`, `types.go`). The evaluated non-test production files hash to combined sha256 `b2135d61f9c84e65b9745c47b118df62e2fd3a9b33328cc45da3b83a3821f682` over `internal/policy/{compile,doc,eval,limits,registry,runtime,time_window,types}.go` and `internal/pricing/{compile,doc,nanousd,price_multiplier,quote,receipt,types}.go`; the tracked working-tree production diff over those two packages (tests excluded) hashes to `aba2c7535c7adbfea715ebc77118fcbc1e263b4ac6d533404e7b47f1e0c734b5`. Because the tree is uncommitted there is **no repository commit or publication/revision identifier** for the measured snapshot.

## 1. What this report measures — and what it does not

Measured (pure evaluation):

- `CompiledConfig.EvalScheduling` — empty config, disabled-only config, rule scans, first-match short circuit, bounded all/any `unknown` worst case.
- `CompiledConfig.EvalPricing` — all rules hit, ordered factor-chain construction.
- `RuntimeView.EvalCandidate` — per-candidate scheduling admission over a group policy.
- Quota window `min` scan (`scanQuotaMin`) via a quota rule (synthetic measured windows only; see the fixture caveat in §2).
- `CompiledConfig.Inspect` — full diagnostic tree generation.
- Concurrent immutable reads of one compiled config via `b.RunParallel` (aggregate parallel throughput, not single-request latency; see §4).

Explicitly **out of scope** (do not read these numbers as end-to-end gateway cost):

- Live request latency, network/upstream I/O, database or key-store I/O.
- Publication locks, atomic pointer swap, `CompileRuntimeView`, registry read/write locks, revision publication.
- Scheduler fairness accounting, affinity cache behavior, `Iterator.Next`, replay/charge paths.
- Affinity stress, multi-tenant concurrency contention, cold caches, GC pressure under sustained load.
- P95/P99 end-to-end latency and throughput under mixed production traffic.

Also, the empty/disabled paths still execute a function call and branch. The sub-nanosecond-near values below are at the Go benchmark loop's measurement floor and are **not** a claim of zero overhead anywhere in the gateway.

## 2. Method

- Toolchain: `go1.27.0 linux/amd64` (`GOOS=linux`, `GOARCH=amd64`, `GOAMD64=v1`, `GOTOOLCHAIN=auto`).
- CPU: AMD Ryzen 9 9950X 16-Core Processor, 32 logical CPUs visible to the benchmark.
- Command:

  ```
  go test ./internal/policy/ -run '^$' \
    -bench '^Benchmark(EvalSchedulingEmptyDisabled|EvalSchedulingRuleCount|EvalPricingAllHits|EvalCandidateCount|QuotaWindowScan|EvalUnknownWorstCase|InspectRuleCount|ConcurrentSchedulingReads)$' \
    -benchmem -benchtime=200ms -count=3
  ```

- `-benchtime=200ms`, `-count=3`. Setup and `Compile(...)`/`CompileRuntimeView(...)` run outside the timer via `b.ResetTimer`; `b.ReportAllocs()` is enabled.
- Numbers below are the median of the three runs. Full raw output is in section 6.
- Fixtures are compiled through the real `Compile` entrypoint and their semantics (hit/miss/unknown/excluded) are asserted by `TestPolicyBenchmarkFixturesValid`, so the benchmarks measure the contract behavior and not an accidental `unknown`; the unknown fixtures additionally assert `RuleStatusSkippedUnknown` through `Inspect`.
- Dimensions are measured independently (rule count, candidate count, window count, tree shape). No rule × candidate × window Cartesian product was evaluated.
- Quota fixture caveat: the quota benchmarks use synthetic `QuotaWindowFact` values with `Ratio` and `State=FactStateMeasured` but zero `ResetAt`/`ObservedAt`. `scanQuotaMin` therefore skips its temporal and reset checks (a missing reset/observation is only treated as unknown by the normalizing producer, not by the pure evaluator). These cases measure scan cost over N matching windows only; they are not normalized production facts and not a window-lifecycle or normalization benchmark.

Environment caveats: single host, single process, no CPU pinning, no `runtime.GC()` between repetitions, default `GOGC` (100). Results are order-of-magnitude guidance, not a portable contract.

## 3. Results (median of 3)

### Scheduling

| Case | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| empty config | 1.176 | 0 | 0 |
| 100 disabled rules | 101.7 | 0 | 0 |
| rules=1, first exclude | 15.65 | 48 | 1 |
| rules=1, scan all | 4.593 | 0 | 0 |
| rules=10, first exclude | 15.19 | 48 | 1 |
| rules=10, scan all | 36.42 | 0 | 0 |
| rules=100, first exclude | 19.43 | 48 | 1 |
| rules=100, scan all | 404.5 | 0 | 0 |

### Pricing (all rules hit)

| Case | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| rules=1 | 34.36 | 96 | 1 |
| rules=10 | 485.8 | 3360 | 5 |
| rules=100 | 4501 | 32032 | 8 |

### Candidate admission (`RuntimeView.EvalCandidate`)

| Case | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| candidates=1 | 9.740 | 0 | 0 |
| candidates=10 | 99.89 | 0 | 0 |
| candidates=50 | 474.6 | 0 | 0 |

### Quota window scan (synthetic measured windows; temporal/reset checks skipped, see §2)

| Case | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| windows=1 | 9.311 | 0 | 0 |
| windows=4 | 20.63 | 0 | 0 |
| windows=16 | 73.78 | 0 | 0 |

### Bounded `unknown` worst case (no short circuit)

| Case | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `all`, 100 unknown leaves | 281.4 | 0 | 0 |
| `any`, 100 unknown leaves | 261.1 | 0 | 0 |
| nested `all`, depth 16 | 36.79 | 0 | 0 |

### Inspect diagnostic tree (3 condition nodes per rule)

| Case | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| rules=1 | 89.07 | 384 | 2 |
| rules=10 | 782.0 | 3808 | 11 |
| rules=100 | 7623 | 38080 | 101 |

### Concurrent immutable reads (aggregate `b.RunParallel` throughput per operation, 32 logical CPUs — not single-request latency)

| Case | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `EvalScheduling` scan-all, `b.RunParallel` | 2.684 | 0 | 0 |

## 4. Observations

- **Empty/disabled fast path is cheap but not free.** An empty config short-circuits before any tree walk; a disabled-only config still iterates its rule slice (about 1 ns per disabled rule here). The evaluator allocates nothing on these paths.
- **Scheduling short-circuits on the first true rule; an exclusion additionally builds one reason snapshot.** First-exclude cost is ~15–20 ns/op and roughly constant across rules=1/10/100 (differences within run-to-run noise) because evaluation stops at the first rule and then allocates the returned `*SchedulingMatch` reason (rule ID, name snapshot): 48 B/op, 1 alloc/op. This is **not equivalent** to the rules=1 scan-all case (4.593 ns/op, 0 alloc/op): scan-all returns no reason, so the first-exclude path pays extra for the reason snapshot plus the exclusion branch. Full scan with no exclusion (scan-all) grows roughly linearly with rule count: 4.6 / 36 / 405 ns/op for 1 / 10 / 100 rules.
- **Pricing accumulates an ordered match slice**, so cost and allocation grow with the number of hits. At 100 hits it is ~4.5 µs/op and ~32 KiB/op. The slice starts at capacity 0 (`make([]PricingMatch, 0)`), so growth reallocates; allocation count (1 → 5 → 8) reflects slice growth, not per-rule allocation.
- **Candidate admission is linear and allocation-free** when no credential-level policy is bound (map miss + group rule evaluation): ~9.7 ns per candidate, 0 allocs across 1/10/50 candidates.
- **Quota `min` scan is linear in matching windows and allocation-free**: ~9.3 / 20.6 / 73.8 ns/op for 1 / 4 / 16 windows. Fixture caveat (see §2): windows are synthetic `Ratio`+`State=Measured` facts with zero `ResetAt`/`ObservedAt`, so the temporal/reset checks are skipped; this measures scan cost, not normalization, freshness, reset, or production window lifecycle.
- **Bounded unknown trees have no short circuit** by design: 100 unknown `all`/`any` leaves evaluate to `unknown` after visiting all children (~255–290 ns/op), and remain 0 alloc. Depth-16 nesting is cheap (~37 ns/op). The fixture test asserts these configs report `RuleStatusSkippedUnknown` via `Inspect`, not merely `Excluded=false`.
- **`Inspect` is the allocation-heavy path** (full diagnostic tree, unknown reasons). For this fixture each rule is `all{ request.model eq "Astra"; request.model eq "Luna" }` = **3 condition nodes per rule**, so rules=1/10/100 evaluate 3/30/300 nodes. Measured: 384 / 3808 / 38080 B/op and 2 / 11 / 101 allocs/op for 1 / 10 / 100 rules, i.e. ~381–384 B/op per rule (~127 B per node) and about one allocation per rule plus the top-level result slice. This matches the contract that diagnostic trees are generated on demand, not on the hot path.
- **Concurrent reads are allocation-free and race-free for this fixture, but one CPU-count sample cannot establish scaling.** The 2.684 ns/op figure is *aggregate `b.RunParallel` throughput normalized per operation across the 32 logical CPUs*, not single-request latency, so it is lower than the single-threaded scan-all by construction. This fixture shows no crash or data race for concurrent reads of one shared immutable `CompiledConfig` (the race detector was run separately); a single logical-CPU count cannot demonstrate flatness, linear scaling, or the absence of a contention cliff under other thread counts or workloads.

## 5. Risks and limits (no production optimization performed)

- This change adds **no** production optimization and does not alter any evaluation code. The report is descriptive.
- Provenance is content-addressed, not commit-addressed: the measured tree was uncommitted, so the reconstruction hashes in the header identify the evaluated files but there is no repository commit or publication/revision identifier. If any evaluated production file changes, these numbers no longer describe the then-current code.
- The concurrency figure comes from a single logical-CPU count (32) on one machine and is aggregate parallel throughput per operation, not single-request latency. It cannot establish scaling behavior or the absence of a contention cliff; the race check was a separate run and is not reproduced in this report.
- All numbers come from one machine and one Go toolchain. They are not portable performance guarantees and should not be promoted to acceptance thresholds without re-measurement on target hardware.
- The pricing slice growth at high hit counts is an observed allocation characteristic, not a measured production bottleneck. Any preallocation change would be a separate, reviewed production change with its own before/after evidence.
- The `Inspect` path scales linearly with rules × nodes and allocates a full tree. Preview/candidate budgets are enforced upstream (`maxPreviewCandidates`, `maxPreviewTargets`, `maxPreviewTotalNodes`); those guards, not these numbers, bound worst-case preview work.
- No data here covers live latency, publication/atomicity, lock waits, scheduler fairness/session admission integration, affinity invalidation, or gateway throughput. Claims about the full gateway cannot be derived from this report.
- The empty/fast-path figures are close to the benchmark loop's overhead floor; treat them as qualitative.

## 6. Raw output

Command: `go test ./internal/policy/ -run '^$' -bench '<see section 2>' -benchmem -benchtime=200ms -count=3`

```
goos: linux
goarch: amd64
pkg: gpt-load/internal/policy
cpu: AMD Ryzen 9 9950X 16-Core Processor
BenchmarkEvalSchedulingEmptyDisabled/empty-32         	220736641	         1.114 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingEmptyDisabled/empty-32         	206906181	         1.176 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingEmptyDisabled/empty-32         	194965932	         1.240 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingEmptyDisabled/disabled_100-32  	 2356568	       101.9 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingEmptyDisabled/disabled_100-32  	 2370720	       101.7 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingEmptyDisabled/disabled_100-32  	 2374454	       101.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=1/first_exclude-32         	15535609	        15.65 ns/op	      48 B/op	       1 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=1/first_exclude-32         	16440037	        17.24 ns/op	      48 B/op	       1 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=1/first_exclude-32         	16360957	        14.62 ns/op	      48 B/op	       1 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=1/scan_all-32              	52698873	         4.606 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=1/scan_all-32              	50932293	         4.593 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=1/scan_all-32              	51575857	         4.539 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=10/first_exclude-32        	14705727	        15.28 ns/op	      48 B/op	       1 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=10/first_exclude-32        	16841721	        15.19 ns/op	      48 B/op	       1 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=10/first_exclude-32        	16540712	        15.01 ns/op	      48 B/op	       1 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=10/scan_all-32             	 6564650	        36.42 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=10/scan_all-32             	 6314206	        37.54 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=10/scan_all-32             	 6608355	        36.34 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=100/first_exclude-32       	14355357	        19.43 ns/op	      48 B/op	       1 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=100/first_exclude-32       	11744276	        20.02 ns/op	      48 B/op	       1 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=100/first_exclude-32       	12204481	        17.30 ns/op	      48 B/op	       1 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=100/scan_all-32            	  604287	       410.4 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=100/scan_all-32            	  565852	       404.5 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalSchedulingRuleCount/rules=100/scan_all-32            	  587743	       404.2 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalPricingAllHits/rules=1-32                            	 6508600	        32.40 ns/op	      96 B/op	       1 allocs/op
BenchmarkEvalPricingAllHits/rules=1-32                            	 6894048	        37.13 ns/op	      96 B/op	       1 allocs/op
BenchmarkEvalPricingAllHits/rules=1-32                            	 6129543	        34.36 ns/op	      96 B/op	       1 allocs/op
BenchmarkEvalPricingAllHits/rules=10-32                           	  636890	       485.8 ns/op	    3360 B/op	       5 allocs/op
BenchmarkEvalPricingAllHits/rules=10-32                           	  543854	       429.7 ns/op	    3360 B/op	       5 allocs/op
BenchmarkEvalPricingAllHits/rules=10-32                           	  498871	       542.0 ns/op	    3360 B/op	       5 allocs/op
BenchmarkEvalPricingAllHits/rules=100-32                          	   48428	      4513 ns/op	   32032 B/op	       8 allocs/op
BenchmarkEvalPricingAllHits/rules=100-32                          	   65388	      3531 ns/op	   32032 B/op	       8 allocs/op
BenchmarkEvalPricingAllHits/rules=100-32                          	   52201	      4501 ns/op	   32032 B/op	       8 allocs/op
BenchmarkEvalCandidateCount/candidates=1-32                       	24587044	         9.590 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalCandidateCount/candidates=1-32                       	24474200	         9.740 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalCandidateCount/candidates=1-32                       	24459024	         9.753 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalCandidateCount/candidates=10-32                      	 2372454	       101.5 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalCandidateCount/candidates=10-32                      	 2415188	        99.49 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalCandidateCount/candidates=10-32                      	 2378344	        99.89 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalCandidateCount/candidates=50-32                      	  493772	       471.5 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalCandidateCount/candidates=50-32                      	  480697	       474.6 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalCandidateCount/candidates=50-32                      	  504252	       478.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkQuotaWindowScan/windows=1-32                             	24421890	         9.277 ns/op	       0 B/op	       0 allocs/op
BenchmarkQuotaWindowScan/windows=1-32                             	24993399	         9.333 ns/op	       0 B/op	       0 allocs/op
BenchmarkQuotaWindowScan/windows=1-32                             	25037833	         9.311 ns/op	       0 B/op	       0 allocs/op
BenchmarkQuotaWindowScan/windows=4-32                             	11117941	        21.53 ns/op	       0 B/op	       0 allocs/op
BenchmarkQuotaWindowScan/windows=4-32                             	11568728	        20.63 ns/op	       0 B/op	       0 allocs/op
BenchmarkQuotaWindowScan/windows=4-32                             	11368078	        20.62 ns/op	       0 B/op	       0 allocs/op
BenchmarkQuotaWindowScan/windows=16-32                            	 3295326	        73.49 ns/op	       0 B/op	       0 allocs/op
BenchmarkQuotaWindowScan/windows=16-32                            	 3244220	        74.16 ns/op	       0 B/op	       0 allocs/op
BenchmarkQuotaWindowScan/windows=16-32                            	 3268519	        73.78 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalUnknownWorstCase/all_unknown_100-32                  	  782641	       292.0 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalUnknownWorstCase/all_unknown_100-32                  	  811806	       263.7 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalUnknownWorstCase/all_unknown_100-32                  	  825630	       281.4 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalUnknownWorstCase/any_unknown_100-32                  	  848077	       261.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalUnknownWorstCase/any_unknown_100-32                  	  788396	       263.4 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalUnknownWorstCase/any_unknown_100-32                  	  842328	       255.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalUnknownWorstCase/nested_unknown_depth_16-32          	 5367656	        38.95 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalUnknownWorstCase/nested_unknown_depth_16-32          	 6913813	        34.66 ns/op	       0 B/op	       0 allocs/op
BenchmarkEvalUnknownWorstCase/nested_unknown_depth_16-32          	 6418902	        36.79 ns/op	       0 B/op	       0 allocs/op
BenchmarkInspectRuleCount/rules=1-32                              	 2475332	        92.78 ns/op	     384 B/op	       2 allocs/op
BenchmarkInspectRuleCount/rules=1-32                              	 2698939	        88.23 ns/op	     384 B/op	       2 allocs/op
BenchmarkInspectRuleCount/rules=1-32                              	 2711884	        89.07 ns/op	     384 B/op	       2 allocs/op
BenchmarkInspectRuleCount/rules=10-32                             	  320103	       781.2 ns/op	    3808 B/op	      11 allocs/op
BenchmarkInspectRuleCount/rules=10-32                             	  314320	       782.0 ns/op	    3808 B/op	      11 allocs/op
BenchmarkInspectRuleCount/rules=10-32                             	  309294	       806.7 ns/op	    3808 B/op	      11 allocs/op
BenchmarkInspectRuleCount/rules=100-32                            	   30252	      7623 ns/op	   38080 B/op	     101 allocs/op
BenchmarkInspectRuleCount/rules=100-32                            	   31998	      7484 ns/op	   38080 B/op	     101 allocs/op
BenchmarkInspectRuleCount/rules=100-32                            	   31047	      8029 ns/op	   38080 B/op	     101 allocs/op
BenchmarkConcurrentSchedulingReads-32                             	91768929	         2.684 ns/op	       0 B/op	       0 allocs/op
BenchmarkConcurrentSchedulingReads-32                             	90611532	         2.682 ns/op	       0 B/op	       0 allocs/op
BenchmarkConcurrentSchedulingReads-32                             	82760418	         2.684 ns/op	       0 B/op	       0 allocs/op
PASS
ok  	gpt-load/internal/policy	17.498s
```
