package codexrecovery

import (
	"testing"
	"time"
)

func TestInvalidLineageIsGroupScopedAndExpires(t *testing.T) {
	lineage := NewInvalidLineage()
	now := time.Unix(1_700_000_000, 0)
	keyA := InvalidLineageKey(1, "session-a")
	keyB := InvalidLineageKey(2, "session-a")

	d1 := EncryptedContentDigest("opaque-a")
	d2 := EncryptedContentDigest("opaque-b")
	if added := lineage.Mark(keyA, []string{d1, d2}, now); added != 2 {
		t.Fatalf("added digests = %d, want 2", added)
	}
	if added := lineage.MarkItemIDs(keyA, []string{"msg_old", "rs_old"}, now); added != 2 {
		t.Fatalf("added item IDs = %d, want 2", added)
	}
	if got := lineage.Lookup(keyA, now.Add(time.Minute)); len(got) != 2 {
		t.Fatalf("group A digests = %d, want 2", len(got))
	}
	if got := lineage.Lookup(keyB, now.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("group B inherited group A digests: %v", got)
	}
	if got := lineage.LookupItemIDs(keyA, now.Add(time.Minute)); len(got) != 2 {
		t.Fatalf("group A item IDs = %d, want 2", len(got))
	}
	if got := lineage.LookupItemIDs(keyB, now.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("group B inherited group A item IDs: %v", got)
	}
	if got := lineage.Lookup(keyA, now.Add(InvalidLineageTTL+time.Second)); len(got) != 0 {
		t.Fatalf("expired lineage remained: %v", got)
	}
	if got := lineage.LookupItemIDs(keyA, now.Add(InvalidLineageTTL+time.Second)); len(got) != 0 {
		t.Fatalf("expired item-ID lineage remained: %v", got)
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

func TestHistoryItemIDsCollectsReplayableItemsAndReferences(t *testing.T) {
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
	if len(ids) != 3 {
		t.Fatalf("history item IDs = %v, want 3", ids)
	}
}
