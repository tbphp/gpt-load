package codexrecovery

import (
	"fmt"
	"testing"
	"time"
)

func TestInvalidLineageIsCredentialScopedAndExpires(t *testing.T) {
	lineage := NewInvalidLineage()
	now := time.Unix(1_700_000_000, 0)
	keyA := InvalidLineageKey(1, 1, "session-a")
	keyB := InvalidLineageKey(2, 1, "session-a")

	d1 := EncryptedContentDigest("opaque-a")
	d2 := EncryptedContentDigest("opaque-b")
	if added := lineage.Mark(keyA, []string{d1, d2}, now); added != 2 {
		t.Fatalf("added digests = %d, want 2", added)
	}
	if added := lineage.MarkItemIDs(keyA, []string{"msg_old", "rs_old"}, now); added != 2 {
		t.Fatalf("added item IDs = %d, want 2", added)
	}
	if got := lineage.Lookup(keyA, now.Add(time.Minute)); len(got) != 2 {
		t.Fatalf("credential A digests = %d, want 2", len(got))
	}
	if got := lineage.Lookup(keyB, now.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("credential B inherited credential A digests: %v", got)
	}
	if got := lineage.Lookup(InvalidLineageKey(1, 2, "session-a"), now.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("a re-authorized credential inherited old digests: %v", got)
	}
	if got := lineage.LookupItemIDs(keyA, now.Add(time.Minute)); len(got) != 2 {
		t.Fatalf("credential A item IDs = %d, want 2", len(got))
	}
	if got := lineage.LookupItemIDs(keyB, now.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("credential B inherited credential A item IDs: %v", got)
	}
	if got := lineage.Lookup(keyA, now.Add(InvalidLineageTTL+2*time.Minute)); len(got) != 0 {
		t.Fatalf("expired lineage remained: %v", got)
	}
	if got := lineage.LookupItemIDs(keyA, now.Add(InvalidLineageTTL+3*time.Minute)); len(got) != 0 {
		t.Fatalf("expired item-ID lineage remained: %v", got)
	}
}

func TestInvalidLineageUsesIdleTTLForActiveSessions(t *testing.T) {
	lineage := NewInvalidLineage()
	now := time.Unix(1_700_000_000, 0)
	key := InvalidLineageKey(1, 1, "session-a")
	if added := lineage.Mark(key, []string{EncryptedContentDigest("opaque")}, now); added != 1 {
		t.Fatalf("added digests = %d, want 1", added)
	}
	if got := lineage.Lookup(key, now.Add(59*time.Minute)); len(got) != 1 {
		t.Fatalf("active lineage expired too early: %v", got)
	}
	if got := lineage.Lookup(key, now.Add(118*time.Minute)); len(got) != 1 {
		t.Fatalf("active lineage did not renew idle TTL: %v", got)
	}
	if got := lineage.Lookup(key, now.Add(179*time.Minute)); len(got) != 0 {
		t.Fatalf("idle lineage remained: %v", got)
	}
}

func TestEncryptedContentDigestsCollectNestedValues(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-1"},
			{"type":"function_call_output","call_id":"call_1","output":[{"type":"encrypted_content","encrypted_content":"opaque-2"}]}
		]
	}`)
	digests := EncryptedContentDigests(body)
	if len(digests) != 2 {
		t.Fatalf("digests = %v, want 2", digests)
	}
}

func TestHistoryItemIDsCollectsReplayableItems(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","id":"msg_1","role":"assistant","content":"history"},
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"summary"}]},
			{"type":"item_reference","id":"rs_server_only"},
			{"type":"web_search_call","id":"ws_unknown"},
			{"type":"message","id":"msg_no_content","role":"user"}
		]
	}`)
	ids := HistoryItemIDs(body)
	if len(ids) != 2 {
		t.Fatalf("history item IDs = %v, want only the 2 replayable items", ids)
	}
}

func TestInvalidLineageKeepsLongSessionsWithinGlobalBound(t *testing.T) {
	lineage := NewInvalidLineage()
	now := time.Unix(1_700_000_000, 0)
	longSession := InvalidLineageKey(1, 1, "session-long")
	values := make([]string, 0, invalidLineageMaxValues+10)
	for index := range 200 {
		values = append(values, EncryptedContentDigest(fmt.Sprintf("opaque-%d", index)))
	}
	if added := lineage.Mark(longSession, values, now); added != 200 {
		t.Fatalf("long session remembered %d digests, want 200", added)
	}

	values = values[:0]
	for index := range invalidLineageMaxValues + 10 {
		values = append(values, fmt.Sprintf("value-%d", index))
	}
	otherSession := InvalidLineageKey(1, 1, "session-other")
	lineage.Mark(otherSession, values, now)
	stored := len(lineage.Lookup(longSession, now)) + len(lineage.Lookup(otherSession, now))
	if stored != invalidLineageMaxValues {
		t.Fatalf("stored values = %d, want the global bound %d", stored, invalidLineageMaxValues)
	}
}
