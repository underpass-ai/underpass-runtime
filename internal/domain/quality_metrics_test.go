package domain

import "testing"

func TestComputeInvocationQuality_Succeeded(t *testing.T) {
	inv := Invocation{
		ID:         "inv-1",
		ToolName:   "fs.write_file",
		Status:     InvocationStatusSucceeded,
		DurationMS: 45,
		ExitCode:   0,
	}
	m, err := ComputeInvocationQuality(inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ToolName() != "fs.write_file" {
		t.Fatalf("expected fs.write_file, got %s", m.ToolName())
	}
	if m.Status() != InvocationStatusSucceeded {
		t.Fatalf("expected succeeded, got %s", m.Status())
	}
	if m.SuccessRate() != 1.0 {
		t.Fatalf("expected 1.0 success rate, got %f", m.SuccessRate())
	}
	if m.LatencyBucket() != "fast" {
		t.Fatalf("expected fast, got %s", m.LatencyBucket())
	}
	if m.HasError() {
		t.Fatal("expected no error")
	}
}

func TestComputeInvocationQuality_Failed(t *testing.T) {
	inv := Invocation{
		ID:         "inv-2",
		ToolName:   "git.push",
		Status:     InvocationStatusFailed,
		DurationMS: 3500,
		ExitCode:   1,
		Error:      &Error{Code: "exec_failed", Message: "push rejected"},
	}
	m, err := ComputeInvocationQuality(inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.SuccessRate() != 0.0 {
		t.Fatalf("expected 0.0, got %f", m.SuccessRate())
	}
	if m.LatencyBucket() != "slow" {
		t.Fatalf("expected slow, got %s", m.LatencyBucket())
	}
	if !m.HasError() {
		t.Fatal("expected error")
	}
	if m.ErrorCode() != "exec_failed" {
		t.Fatalf("expected exec_failed, got %s", m.ErrorCode())
	}
}

func TestComputeInvocationQuality_Denied(t *testing.T) {
	inv := Invocation{
		ID:         "inv-3",
		ToolName:   "k8s.apply_manifest",
		Status:     InvocationStatusDenied,
		DurationMS: 2,
		Error:      &Error{Code: "role_denied", Message: "missing admin role"},
	}
	m, err := ComputeInvocationQuality(inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.SuccessRate() != 0.0 {
		t.Fatalf("expected 0.0, got %f", m.SuccessRate())
	}
	if m.LatencyBucket() != "fast" {
		t.Fatalf("expected fast, got %s", m.LatencyBucket())
	}
}

func TestComputeInvocationQuality_Running(t *testing.T) {
	inv := Invocation{ID: "inv-4", Status: InvocationStatusRunning}
	_, err := ComputeInvocationQuality(inv)
	if err == nil {
		t.Fatal("expected error for running invocation")
	}
}

func TestClassifyLatency(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "fast"},
		{100, "fast"},
		{101, "normal"},
		{1000, "normal"},
		{1001, "slow"},
		{5000, "slow"},
		{5001, "very_slow"},
		{60000, "very_slow"},
	}
	for _, tc := range cases {
		got := classifyLatency(tc.ms)
		if got != tc.want {
			t.Errorf("classifyLatency(%d) = %s, want %s", tc.ms, got, tc.want)
		}
	}
}
