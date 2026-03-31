package domain

import (
	"math"
	"testing"
)

func TestIdentityMatrix(t *testing.T) {
	m := identityMatrix(3)
	if m.rows != 3 || m.cols != 3 {
		t.Fatalf("dims = %dx%d, want 3x3", m.rows, m.cols)
	}
	for i := range 3 {
		for j := range 3 {
			want := 0.0
			if i == j {
				want = 1.0
			}
			if m.data[i][j] != want {
				t.Errorf("m[%d][%d] = %f, want %f", i, j, m.data[i][j], want)
			}
		}
	}
}

func TestZeroMatrix(t *testing.T) {
	m := zeroMatrix(2, 3)
	if m.rows != 2 || m.cols != 3 {
		t.Fatalf("dims = %dx%d, want 2x3", m.rows, m.cols)
	}
	for i := range 2 {
		for j := range 3 {
			if m.data[i][j] != 0 {
				t.Errorf("m[%d][%d] = %f, want 0", i, j, m.data[i][j])
			}
		}
	}
}

func TestZeroVector(t *testing.T) {
	v := zeroVector(4)
	if len(v.data) != 4 {
		t.Fatalf("length = %d, want 4", len(v.data))
	}
	for i, val := range v.data {
		if val != 0 {
			t.Errorf("v[%d] = %f, want 0", i, val)
		}
	}
}

func TestNewVector(t *testing.T) {
	original := []float64{1, 2, 3}
	v := newVector(original)

	// Verify copy.
	original[0] = 999
	if v.data[0] != 1 {
		t.Error("newVector should copy, not reference")
	}
}

func TestMatrixSolve(t *testing.T) {
	// A = 2I, b = [4, 6] → x = [2, 3]
	m := identityMatrix(2)
	m.data[0][0] = 2
	m.data[1][1] = 2
	b := &vector{data: []float64{4, 6}}

	x := m.solve(b)
	if math.Abs(x.data[0]-2) > 1e-10 || math.Abs(x.data[1]-3) > 1e-10 {
		t.Errorf("solve = %v, want [2, 3]", x.data)
	}
}

func TestMatrixSolve_ZeroDiagonal(t *testing.T) {
	m := zeroMatrix(2, 2)
	b := &vector{data: []float64{4, 6}}

	x := m.solve(b)
	if x.data[0] != 0 || x.data[1] != 0 {
		t.Errorf("solve with zero diagonal = %v, want [0, 0]", x.data)
	}
}

func TestDiagInverse(t *testing.T) {
	m := identityMatrix(3)
	m.data[0][0] = 2
	m.data[1][1] = 4
	m.data[2][2] = 5

	inv := m.diagInverse()
	if math.Abs(inv[0]-0.5) > 1e-10 || math.Abs(inv[1]-0.25) > 1e-10 || math.Abs(inv[2]-0.2) > 1e-10 {
		t.Errorf("diagInverse = %v, want [0.5, 0.25, 0.2]", inv)
	}
}

func TestDiagInverse_ZeroDiag(t *testing.T) {
	m := zeroMatrix(2, 2)
	inv := m.diagInverse()
	if inv[0] != 0 || inv[1] != 0 {
		t.Errorf("diagInverse of zero = %v, want [0, 0]", inv)
	}
}

func TestAddOuterProduct(t *testing.T) {
	m := zeroMatrix(2, 2)
	x := &vector{data: []float64{1, 2}}
	y := &vector{data: []float64{3, 4}}
	m.addOuterProduct(x, y)

	// [1*3, 1*4; 2*3, 2*4] = [3, 4; 6, 8]
	want := [][]float64{{3, 4}, {6, 8}}
	for i := range 2 {
		for j := range 2 {
			if m.data[i][j] != want[i][j] {
				t.Errorf("m[%d][%d] = %f, want %f", i, j, m.data[i][j], want[i][j])
			}
		}
	}
}

func TestAddMatrix(t *testing.T) {
	a := identityMatrix(2)
	b := identityMatrix(2)
	a.addMatrix(b)
	if a.data[0][0] != 2 || a.data[1][1] != 2 {
		t.Errorf("I+I diagonal = %f, %f, want 2, 2", a.data[0][0], a.data[1][1])
	}
	if a.data[0][1] != 0 {
		t.Errorf("off-diagonal = %f, want 0", a.data[0][1])
	}
}

func TestSubMatrix(t *testing.T) {
	a := identityMatrix(2)
	b := identityMatrix(2)
	a.subMatrix(b)
	for i := range 2 {
		for j := range 2 {
			if a.data[i][j] != 0 {
				t.Errorf("I-I[%d][%d] = %f, want 0", i, j, a.data[i][j])
			}
		}
	}
}

func TestMulVec(t *testing.T) {
	m := identityMatrix(2)
	m.data[0][0] = 2
	m.data[0][1] = 1
	m.data[1][0] = 0
	m.data[1][1] = 3
	v := &vector{data: []float64{3, 4}}

	result := m.mulVec(v)
	// [2*3+1*4, 0*3+3*4] = [10, 12]
	if math.Abs(result.data[0]-10) > 1e-10 || math.Abs(result.data[1]-12) > 1e-10 {
		t.Errorf("mulVec = %v, want [10, 12]", result.data)
	}
}

func TestMulMat(t *testing.T) {
	a := identityMatrix(2)
	a.data[0][0] = 1
	a.data[0][1] = 2
	a.data[1][0] = 3
	a.data[1][1] = 4

	b := identityMatrix(2)
	b.data[0][0] = 5
	b.data[0][1] = 6
	b.data[1][0] = 7
	b.data[1][1] = 8

	c := a.mulMat(b)
	// [1*5+2*7, 1*6+2*8; 3*5+4*7, 3*6+4*8] = [19, 22; 43, 50]
	want := [][]float64{{19, 22}, {43, 50}}
	for i := range 2 {
		for j := range 2 {
			if math.Abs(c.data[i][j]-want[i][j]) > 1e-10 {
				t.Errorf("c[%d][%d] = %f, want %f", i, j, c.data[i][j], want[i][j])
			}
		}
	}
}

func TestTransposeMulDiag(t *testing.T) {
	// M = [[1,2],[3,4],[5,6]] (3x2), diag = [1, 2, 3]
	// M^T * diag(d) = [[1,3,5],[2,4,6]] * diag([1,2,3])
	// = [[1*1, 3*2, 5*3], [2*1, 4*2, 6*3]] = [[1,6,15],[2,8,18]]
	m := zeroMatrix(3, 2)
	m.data[0] = []float64{1, 2}
	m.data[1] = []float64{3, 4}
	m.data[2] = []float64{5, 6}

	diag := []float64{1, 2, 3}
	result := m.transposeMulDiag(diag)

	if result.rows != 2 || result.cols != 3 {
		t.Fatalf("dims = %dx%d, want 2x3", result.rows, result.cols)
	}
	want := [][]float64{{1, 6, 15}, {2, 8, 18}}
	for i := range 2 {
		for j := range 3 {
			if math.Abs(result.data[i][j]-want[i][j]) > 1e-10 {
				t.Errorf("result[%d][%d] = %f, want %f", i, j, result.data[i][j], want[i][j])
			}
		}
	}
}

func TestAddOuterXY(t *testing.T) {
	m := zeroMatrix(2, 3)
	x := &vector{data: []float64{1, 2}}
	y := &vector{data: []float64{3, 4, 5}}
	m.addOuterXY(x, y)

	want := [][]float64{{3, 4, 5}, {6, 8, 10}}
	for i := range 2 {
		for j := range 3 {
			if m.data[i][j] != want[i][j] {
				t.Errorf("m[%d][%d] = %f, want %f", i, j, m.data[i][j], want[i][j])
			}
		}
	}
}

func TestVectorAddScaled(t *testing.T) {
	v := &vector{data: []float64{1, 2, 3}}
	other := &vector{data: []float64{4, 5, 6}}
	v.addScaled(other, 2.0)
	// [1+8, 2+10, 3+12] = [9, 12, 15]
	want := []float64{9, 12, 15}
	for i, w := range want {
		if v.data[i] != w {
			t.Errorf("v[%d] = %f, want %f", i, v.data[i], w)
		}
	}
}

func TestVectorAddVec(t *testing.T) {
	v := &vector{data: []float64{1, 2}}
	other := &vector{data: []float64{3, 4}}
	v.addVec(other)
	if v.data[0] != 4 || v.data[1] != 6 {
		t.Errorf("addVec = %v, want [4, 6]", v.data)
	}
}

func TestVectorSubVec(t *testing.T) {
	v := &vector{data: []float64{5, 3}}
	other := &vector{data: []float64{2, 1}}
	v.subVec(other)
	if v.data[0] != 3 || v.data[1] != 2 {
		t.Errorf("subVec = %v, want [3, 2]", v.data)
	}
}

func TestVectorSub(t *testing.T) {
	v := &vector{data: []float64{5, 3}}
	other := &vector{data: []float64{2, 1}}
	result := v.sub(other)
	if result.data[0] != 3 || result.data[1] != 2 {
		t.Errorf("sub = %v, want [3, 2]", result.data)
	}
	// Original should be unchanged.
	if v.data[0] != 5 {
		t.Error("sub should not mutate receiver")
	}
}

func TestDotProduct(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{4, 5, 6}
	got := dotProduct(a, b)
	// 1*4 + 2*5 + 3*6 = 32
	if got != 32 {
		t.Errorf("dotProduct = %f, want 32", got)
	}
}

func TestQuadFormDiag(t *testing.T) {
	x := []float64{2, 3}
	diagInv := []float64{0.5, 0.25}
	// 2^2*0.5 + 3^2*0.25 = 2 + 2.25 = 4.25
	got := quadFormDiag(x, diagInv)
	if math.Abs(got-4.25) > 1e-10 {
		t.Errorf("quadFormDiag = %f, want 4.25", got)
	}
}

func TestMatrixIdentityMethod(t *testing.T) {
	m := zeroMatrix(2, 2)
	id := m.identity(3)
	if id.rows != 3 || id.cols != 3 {
		t.Fatalf("dims = %dx%d, want 3x3", id.rows, id.cols)
	}
	if id.data[0][0] != 1 || id.data[1][1] != 1 || id.data[2][2] != 1 {
		t.Error("identity diagonal should be 1")
	}
}

func TestHyLinUCB_ConcurrentAccess(t *testing.T) {
	h := NewHyLinUCB(SharedFeatureDim, ArmFeatureDim, 0.25)
	ctx := EncodeContextFeatures(ContextSignature{TaskFamily: "gen", Lang: "go", ConstraintsClass: "std"})
	arm := EncodeToolFeatures("low", "none", "free", false)
	z := EncodeSharedFeatures(ctx, arm)

	done := make(chan struct{})
	go func() {
		for range 100 {
			h.Update("fs.write", ctx, z, 1.0)
		}
		close(done)
	}()

	for range 100 {
		_ = h.Score("fs.write", ctx, z)
	}
	<-done

	if h.ArmCount() < 1 {
		t.Error("should have at least 1 arm")
	}
}

func TestHyLinUCB_GetOrCreateArm_Idempotent(t *testing.T) {
	h := NewHyLinUCB(4, 3, 0.25)

	// Score creates arm implicitly.
	ctx := []float64{1, 0, 0}
	z := []float64{1, 0, 0, 0}
	_ = h.Score("tool-a", ctx, z)
	_ = h.Score("tool-a", ctx, z)

	if h.ArmCount() != 1 {
		t.Errorf("arms = %d, want 1 (should not duplicate)", h.ArmCount())
	}
}

func TestHyLinUCB_MultipleArms(t *testing.T) {
	h := NewHyLinUCB(4, 3, 0.25)
	ctx := []float64{1, 0, 0}
	z := []float64{1, 0, 0, 0}

	h.Update("a", ctx, z, 1.0)
	h.Update("b", ctx, z, 0.0)
	h.Update("c", ctx, z, 1.0)

	if h.ArmCount() != 3 {
		t.Errorf("arms = %d, want 3", h.ArmCount())
	}
}
