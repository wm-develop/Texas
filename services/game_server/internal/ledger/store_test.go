package ledger

import "testing"

func TestInMemoryStoreIsAppendOnlyAndRejectsDuplicateEntries(t *testing.T) {
	store := NewInMemoryStore()
	entries := []Entry{
		{EntryID: "hand_1:a", HandID: "hand_1", PlayerID: "a", Delta: -10, BalanceAfter: 90},
		{EntryID: "hand_1:b", HandID: "hand_1", PlayerID: "b", Delta: 10, BalanceAfter: 110},
	}
	if err := store.Append(entries); err != nil {
		t.Fatalf("Append: %v", err)
	}
	entries[0].Delta = 999
	stored := store.EntriesForHand("hand_1")
	if stored[0].Delta != -10 {
		t.Fatalf("caller mutated stored entry: %#v", stored[0])
	}
	if err := store.Append([]Entry{stored[0]}); err == nil {
		t.Fatal("expected duplicate append to fail")
	}
}
