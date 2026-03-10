package s3

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

func TestObjectKey(t *testing.T) {
	ts := time.Date(2026, 3, 9, 14, 30, 0, 0, time.UTC)
	got := objectKey(ts)
	want := "audit/dt=2026-03-09/hour=14/snapshot-20260309T143000Z.json"
	if got != want {
		t.Errorf("objectKey() = %q, want %q", got, want)
	}
}

func TestObjectKeyMidnight(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := objectKey(ts)
	want := "audit/dt=2026-01-01/hour=00/snapshot-20260101T000000Z.json"
	if got != want {
		t.Errorf("objectKey() = %q, want %q", got, want)
	}
}

func TestSnapshotEnvelopeJSON(t *testing.T) {
	ts := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	envelope := snapshotEnvelope{
		Ts:    ts.Format(time.RFC3339),
		Count: 2,
		Policies: []domain.ToolPolicy{
			{ContextSignature: "gen:go:std", ToolID: "fs.write", Alpha: 91, Beta: 11},
			{ContextSignature: "gen:go:std", ToolID: "fs.read", Alpha: 50, Beta: 1},
		},
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got snapshotEnvelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Count != 2 {
		t.Errorf("count = %d, want 2", got.Count)
	}
	if got.Ts != "2026-03-09T12:00:00Z" {
		t.Errorf("ts = %q, want 2026-03-09T12:00:00Z", got.Ts)
	}
	if len(got.Policies) != 2 {
		t.Fatalf("policies len = %d, want 2", len(got.Policies))
	}
	if got.Policies[0].Alpha != 91 {
		t.Errorf("policies[0].Alpha = %f, want 91", got.Policies[0].Alpha)
	}
}
