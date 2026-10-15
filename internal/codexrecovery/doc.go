// Package codexrecovery implements the OpenAI Responses encrypted-content
// compatibility recovery: when an upstream rejects account-bound
// `encrypted_content` (reasoning / tool output / compaction), the rejected
// opaque state is stripped from the outbound request so the same account can
// replay it once without needing to decrypt state it did not author.
//
// The package is pure: it classifies upstream error evidence and rewrites a
// request body without any IO, so it can be unit tested in isolation.
package codexrecovery
