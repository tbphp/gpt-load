// Package codexrecovery implements failure-triggered OpenAI Responses
// continuation recovery. Explicit item ownership, missing history item, and
// encrypted-content errors trigger a rewrite of self-contained inline history:
// remove upstream item IDs and rejected opaque state, preserving readable
// contents and tool call associations. Normal requests are never rewritten.
// The gateway replays once on the current credential and retains this recovery
// for subsequent candidate failover within the existing retry budget.
// Reference-only history cannot be rebuilt without its original server state.
//
// The package is pure: it classifies upstream error evidence and rewrites a
// request body without any IO, so it can be unit tested in isolation.
package codexrecovery
