package domain

import "testing"

func TestEncodeContextFeatures_AllLanguages(t *testing.T) {
	tests := []struct {
		lang     string
		expected int // index that should be 1.0
	}{
		{"go", 0},
		{"python", 1},
		{"javascript", 2},
		{"rust", 3},
		{"java", 4},
		{"unknown", 5},
		{"", 5},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			sig := ContextSignature{TaskFamily: "gen", Lang: tt.lang, ConstraintsClass: "std"}
			f := EncodeContextFeatures(sig)
			if len(f) != ArmFeatureDim {
				t.Fatalf("length = %d, want %d", len(f), ArmFeatureDim)
			}
			for i := 0; i < 6; i++ {
				expected := 0.0
				if i == tt.expected {
					expected = 1.0
				}
				if f[i] != expected {
					t.Errorf("f[%d] = %f, want %f (lang=%s)", i, f[i], expected, tt.lang)
				}
			}
		})
	}
}

func TestEncodeContextFeatures_TaskFamilies(t *testing.T) {
	tests := []struct {
		taskFamily string
		wantSvc    float64
		wantCLI    float64
		wantLib    float64
	}{
		{"gen", 1, 0, 0},
		{"build", 1, 0, 0},
		{"deploy", 1, 0, 0},
		{"test", 0, 1, 0},
		{"review", 0, 1, 0},
		{"other", 0, 0, 1},
		{"", 0, 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.taskFamily, func(t *testing.T) {
			sig := ContextSignature{TaskFamily: tt.taskFamily, Lang: "go", ConstraintsClass: "std"}
			f := EncodeContextFeatures(sig)
			if f[6] != tt.wantSvc {
				t.Errorf("TypeService = %f, want %f", f[6], tt.wantSvc)
			}
			if f[7] != tt.wantCLI {
				t.Errorf("TypeCLI = %f, want %f", f[7], tt.wantCLI)
			}
			if f[8] != tt.wantLib {
				t.Errorf("TypeLibrary = %f, want %f", f[8], tt.wantLib)
			}
		})
	}
}

func TestEncodeContextFeatures_SecurityClean(t *testing.T) {
	strict := EncodeContextFeatures(ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "strict"})
	if strict[12] != 1.0 {
		t.Errorf("strict SecurityClean = %f, want 1.0", strict[12])
	}

	perf := EncodeContextFeatures(ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "performance"})
	if perf[12] != 1.0 {
		t.Errorf("performance SecurityClean = %f, want 1.0", perf[12])
	}

	std := EncodeContextFeatures(ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "std"})
	if std[12] != 0.0 {
		t.Errorf("std SecurityClean = %f, want 0.0", std[12])
	}
}

func TestEncodeToolFeatures_AllCombinations(t *testing.T) {
	tests := []struct {
		name     string
		risk     string
		side     string
		cost     string
		approval bool
		expected []float64
	}{
		{"all-low", "low", "none", "free", false, []float64{0, 0, 0, 0}},
		{"all-high", "high", "irreversible", "high", true, []float64{1, 1, 1, 1}},
		{"medium", "medium", "reversible", "medium", false, []float64{0.5, 0.5, 0.66, 0}},
		{"low-cost", "low", "none", "low", true, []float64{0, 0, 0.33, 1}},
		{"unknown", "unknown", "unknown", "unknown", false, []float64{0, 0, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EncodeToolFeatures(tt.risk, tt.side, tt.cost, tt.approval)
			if len(got) != 4 {
				t.Fatalf("length = %d, want 4", len(got))
			}
			for i, want := range tt.expected {
				if got[i] != want {
					t.Errorf("got[%d] = %f, want %f", i, got[i], want)
				}
			}
		})
	}
}

func TestEncodeSharedFeatures_Concatenation(t *testing.T) {
	ctx := []float64{1, 2, 3}
	arm := []float64{4, 5}
	z := EncodeSharedFeatures(ctx, arm)

	want := []float64{1, 2, 3, 4, 5}
	if len(z) != len(want) {
		t.Fatalf("length = %d, want %d", len(z), len(want))
	}
	for i, w := range want {
		if z[i] != w {
			t.Errorf("z[%d] = %f, want %f", i, z[i], w)
		}
	}
}

func TestEncodeSharedFeatures_Empty(t *testing.T) {
	z := EncodeSharedFeatures(nil, nil)
	if len(z) != 0 {
		t.Errorf("length = %d, want 0", len(z))
	}
}

func TestContextFeatures_ToSlice_Length(t *testing.T) {
	f := ContextFeatures{LangGo: 1, TypeService: 1}
	s := f.ToSlice()
	if len(s) != 13 {
		t.Errorf("length = %d, want 13", len(s))
	}
	if s[0] != 1.0 {
		t.Error("LangGo not set")
	}
	if s[6] != 1.0 {
		t.Error("TypeService not set")
	}
}

func TestToolFeatures_ToSlice(t *testing.T) {
	f := ToolFeatures{RiskLevel: 0.5, SideEffects: 0.5, CostHint: 0.66, Approval: 1}
	s := f.ToSlice()
	if len(s) != 4 {
		t.Fatalf("length = %d, want 4", len(s))
	}
	if s[0] != 0.5 || s[1] != 0.5 || s[2] != 0.66 || s[3] != 1 {
		t.Errorf("got %v", s)
	}
}
