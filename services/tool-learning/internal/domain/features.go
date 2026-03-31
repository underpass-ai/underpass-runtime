package domain

// ToolFeatures encodes tool metadata into a fixed-size numeric vector
// for use as arm features in HyLinUCB.
type ToolFeatures struct {
	RiskLevel   float64 // 0=low, 0.5=medium, 1=high
	SideEffects float64 // 0=none, 0.5=reversible, 1=irreversible
	CostHint    float64 // 0=free, 0.33=low, 0.66=medium, 1=high
	Approval    float64 // 0=no, 1=yes
}

// ContextFeatures encodes task context into a fixed-size numeric vector
// for use as context features in HyLinUCB.
type ContextFeatures struct {
	LangGo        float64 // 1 if Go project
	LangPython    float64 // 1 if Python
	LangJS        float64 // 1 if JavaScript
	LangRust      float64 // 1 if Rust
	LangJava      float64 // 1 if Java
	LangOther     float64 // 1 if other/unknown
	TypeService   float64 // 1 if service
	TypeCLI       float64 // 1 if CLI
	TypeLibrary   float64 // 1 if library
	HasDockerfile float64 // 1 if Dockerfile present
	HasK8s        float64 // 1 if K8s manifests present
	TestsPassing  float64 // 1 if tests passing, 0 otherwise
	SecurityClean float64 // 1 if clean, 0 if warnings
}

// ArmFeatureDim is the dimension of arm-specific context features (d).
const ArmFeatureDim = 13

// SharedFeatureDim is the dimension of shared features (k = context + arm metadata).
const SharedFeatureDim = 17

// EncodeContextFeatures converts a ContextSignature to a numeric feature vector.
func EncodeContextFeatures(sig ContextSignature) []float64 {
	var f ContextFeatures
	switch sig.Lang {
	case "go":
		f.LangGo = 1
	case "python":
		f.LangPython = 1
	case "javascript":
		f.LangJS = 1
	case "rust":
		f.LangRust = 1
	case "java":
		f.LangJava = 1
	default:
		f.LangOther = 1
	}
	switch sig.TaskFamily {
	case "gen", "build", "deploy":
		f.TypeService = 1
	case "test", "review":
		f.TypeCLI = 1
	default:
		f.TypeLibrary = 1
	}
	if sig.ConstraintsClass == "strict" || sig.ConstraintsClass == "performance" {
		f.SecurityClean = 1
	}
	return f.ToSlice()
}

// EncodeToolFeatures converts tool metadata to a numeric feature vector.
func EncodeToolFeatures(risk, sideEffects, costHint string, requiresApproval bool) []float64 {
	var f ToolFeatures
	switch risk {
	case "low":
		f.RiskLevel = 0
	case "medium":
		f.RiskLevel = 0.5
	case "high":
		f.RiskLevel = 1
	}
	switch sideEffects {
	case "none":
		f.SideEffects = 0
	case "reversible":
		f.SideEffects = 0.5
	case "irreversible":
		f.SideEffects = 1
	}
	switch costHint {
	case "free":
		f.CostHint = 0
	case "low":
		f.CostHint = 0.33
	case "medium":
		f.CostHint = 0.66
	case "high":
		f.CostHint = 1
	}
	if requiresApproval {
		f.Approval = 1
	}
	return f.ToSlice()
}

// EncodeSharedFeatures concatenates context and arm features for the shared
// parameter model (z vector in HyLinUCB).
func EncodeSharedFeatures(ctx []float64, arm []float64) []float64 {
	z := make([]float64, len(ctx)+len(arm))
	copy(z, ctx)
	copy(z[len(ctx):], arm)
	return z
}

// ToSlice converts ContextFeatures to a float64 slice.
func (f ContextFeatures) ToSlice() []float64 {
	return []float64{
		f.LangGo, f.LangPython, f.LangJS, f.LangRust, f.LangJava, f.LangOther,
		f.TypeService, f.TypeCLI, f.TypeLibrary,
		f.HasDockerfile, f.HasK8s,
		f.TestsPassing, f.SecurityClean,
	}
}

// ToSlice converts ToolFeatures to a float64 slice.
func (f ToolFeatures) ToSlice() []float64 {
	return []float64{f.RiskLevel, f.SideEffects, f.CostHint, f.Approval}
}
