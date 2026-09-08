package reconciler

import (
	"encoding/json"
	"testing"
)

func TestDecodePollMemory_fresh(t *testing.T) {
	m := DecodePollMemory(nil)
	if m.LegacySeen {
		t.Fatal("empty file should not be treated as a legacy upgrade")
	}
	if len(m.NotifiedNew) != 0 || len(m.AppliedSHAs) != 0 {
		t.Fatalf("got notified=%v applied=%v", m.NotifiedNew, m.AppliedSHAs)
	}
}

func TestDecodePollMemory_legacySeenPRs(t *testing.T) {
	data := []byte(`{
  "timestamp": "2026-08-01T00:00:00Z",
  "pr_count": 2,
  "seen_prs": ["18", "42"]
}`)
	m := DecodePollMemory(data)
	if !m.LegacySeen {
		t.Fatal("seen_prs without notified_new should set LegacySeen")
	}
	if len(m.NotifiedNew) != 0 {
		t.Fatal("number-only seen_prs cannot be mapped to repo:n keys")
	}
}

func TestDecodePollMemory_newFormat(t *testing.T) {
	data := []byte(`{
  "timestamp": "2026-08-27T00:00:00Z",
  "pr_count": 1,
  "applied_shas": {"zen:18": "abc1234"},
  "notified_new": ["zen:18"]
}`)
	m := DecodePollMemory(data)
	if m.LegacySeen {
		t.Fatal("new format should not set LegacySeen")
	}
	if !m.NotifiedNew["zen:18"] {
		t.Fatal("expected notified_new key zen:18")
	}
	if m.AppliedSHAs["zen:18"] != "abc1234" {
		t.Fatalf("applied sha = %q", m.AppliedSHAs["zen:18"])
	}
}

func TestDecodePollMemory_mixedOldAndNew(t *testing.T) {
	data := []byte(`{"seen_prs":["18"],"notified_new":["zen:18"]}`)
	m := DecodePollMemory(data)
	if m.LegacySeen {
		t.Fatal("notified_new present means this is already the new format")
	}
}

func TestEncodeCheckFile_dropsSeenPRs(t *testing.T) {
	m := PollMemory{
		NotifiedNew: map[string]bool{"zen:18": true},
		AppliedSHAs: map[string]string{"zen:18": "deadbeef"},
		LegacySeen:  true,
	}
	raw, err := EncodeCheckFile(m, "2026-08-27T00:00:00Z", 1)
	if err != nil {
		t.Fatal(err)
	}
	var file CheckFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.SeenPRs) != 0 {
		t.Fatalf("seen_prs should not be written after upgrade: %v", file.SeenPRs)
	}
	if len(file.NotifiedNew) != 1 || file.AppliedSHAs["zen:18"] != "deadbeef" {
		t.Fatalf("file = %+v", file)
	}
}
