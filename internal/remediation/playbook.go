package remediation

// Playbook defines an automated remediation workflow triggered by an alert.
type Playbook struct {
	AlertName    string   // matches AlertEvent.AlertName
	Description  string   // human-readable purpose
	DiagnoseHint string   // task hint for RecommendTools (diagnosis phase)
	Tools        []string // tools to invoke in order
	Severity     string   // minimum severity to trigger (warning, critical)
}

// DefaultPlaybooks returns the built-in remediation playbooks.
func DefaultPlaybooks() []Playbook {
	return []Playbook{
		{
			AlertName:    "WorkspaceInvocationFailureRateHigh",
			Description:  "Diagnose high tool failure rate and identify root cause",
			DiagnoseHint: "diagnose tool failures and error patterns",
			Tools:        []string{"fs.list", "fs.read_file"},
			Severity:     "warning",
		},
		{
			AlertName:    "WorkspaceP95InvocationLatencyHigh",
			Description:  "Investigate latency spikes in tool invocations",
			DiagnoseHint: "investigate slow tool execution and resource usage",
			Tools:        []string{"fs.list"},
			Severity:     "warning",
		},
		{
			AlertName:    "WorkspaceInvocationDeniedRateHigh",
			Description:  "Audit policy denials and identify misconfigured policies",
			DiagnoseHint: "audit policy denials and authorization issues",
			Tools:        []string{"fs.list"},
			Severity:     "warning",
		},
		{
			AlertName:    "WorkspaceDown",
			Description:  "Emergency: workspace service unreachable",
			DiagnoseHint: "check service health and connectivity",
			Tools:        []string{},
			Severity:     "critical",
		},
	}
}

// MatchPlaybook finds the first playbook matching an alert name.
func MatchPlaybook(playbooks []Playbook, alertName string) (Playbook, bool) {
	for _, pb := range playbooks {
		if pb.AlertName == alertName {
			return pb, true
		}
	}
	return Playbook{}, false
}
