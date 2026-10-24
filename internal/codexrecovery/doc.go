// Package codexrecovery implements failure-triggered OpenAI Responses
// continuation recovery. Explicit item ownership, missing history item, and
// encrypted-content errors trigger a rewrite of self-contained inline history:
// remove upstream item IDs and rejected opaque state, preserving readable
// contents and tool call associations. Compaction items and server-side item
// references hold history that cannot be rebuilt here, so they are never
// removed. Normal requests are never rewritten.
// The gateway replays once on the current credential within the retry budget
// and retains this recovery for subsequent candidate failover.
// Rejected history item IDs and encrypted-content digests are also remembered
// per client session and credential, so later turns remove only the
// already-rejected state before contacting the upstream instead of repeating
// the same failure.
//
// The package is pure: it classifies upstream error evidence and rewrites a
// request body without any IO, so it can be unit tested in isolation.
package codexrecovery
