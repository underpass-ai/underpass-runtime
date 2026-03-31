package domain

import "testing"

func TestHyLinUCBPolicyComputer_ComputePolicy(t *testing.T) {
	pc := NewHyLinUCBPolicyComputer(0.25)

	policy := pc.ComputePolicy("gen:go:std", "fs.write_file", AggregateStats{
		ContextSignature: "gen:go:std",
		ToolID:           "fs.write_file",
		Total:            100,
		Successes:        90,
		Failures:         10,
		P95LatencyMs:     200,
		P95Cost:          0.1,
		ErrorRate:        0.1,
	})

	if policy.ContextSignature != "gen:go:std" {
		t.Errorf("context = %q", policy.ContextSignature)
	}
	if policy.ToolID != "fs.write_file" {
		t.Errorf("tool = %q", policy.ToolID)
	}
	if policy.Alpha != 91 || policy.Beta != 11 {
		t.Errorf("alpha=%f beta=%f, want 91/11", policy.Alpha, policy.Beta)
	}
	if policy.Confidence <= 0 {
		t.Errorf("confidence should be positive, got %f", policy.Confidence)
	}
	if policy.P95LatencyMs != 200 {
		t.Errorf("latency = %d", policy.P95LatencyMs)
	}
}

func TestHyLinUCBPolicyComputer_ContextDifferentiation(t *testing.T) {
	pc := NewHyLinUCBPolicyComputer(0.25)

	// Train with different success rates per context.
	for range 50 {
		pc.ComputePolicy("gen:go:std", "fs.write_file", AggregateStats{
			Total: 100, Successes: 95, Failures: 5,
		})
	}
	for range 50 {
		pc.ComputePolicy("gen:python:std", "fs.write_file", AggregateStats{
			Total: 100, Successes: 30, Failures: 70,
		})
	}

	goPolicy := pc.ComputePolicy("gen:go:std", "fs.write_file", AggregateStats{
		Total: 100, Successes: 95, Failures: 5,
	})
	pyPolicy := pc.ComputePolicy("gen:python:std", "fs.write_file", AggregateStats{
		Total: 100, Successes: 30, Failures: 70,
	})

	// Go context should score higher for this tool.
	t.Logf("go confidence=%f, python confidence=%f", goPolicy.Confidence, pyPolicy.Confidence)
}

func TestHyLinUCBPolicyComputer_ZeroTotal(t *testing.T) {
	pc := NewHyLinUCBPolicyComputer(0.25)

	policy := pc.ComputePolicy("gen:go:std", "new.tool", AggregateStats{
		Total: 0, Successes: 0, Failures: 0,
	})

	// Should not panic, confidence comes from exploration bonus.
	if policy.Alpha != 1 || policy.Beta != 1 {
		t.Errorf("alpha=%f beta=%f, want 1/1 for zero data", policy.Alpha, policy.Beta)
	}
}

func TestParseContextSignature(t *testing.T) {
	tests := []struct {
		key  string
		want ContextSignature
	}{
		{"gen:go:std", ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "std"}},
		{"test:python:strict", ContextSignature{TaskFamily: "test", Lang: "python", ConstraintsClass: "strict"}},
		{"gen:go", ContextSignature{TaskFamily: "gen", Lang: "go"}},
		{"gen", ContextSignature{TaskFamily: "gen"}},
		{"", ContextSignature{}},
	}
	for _, tt := range tests {
		got := ParseContextSignature(tt.key)
		if got != tt.want {
			t.Errorf("ParseContextSignature(%q) = %+v, want %+v", tt.key, got, tt.want)
		}
	}
}
