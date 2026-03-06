package telemetry

// Savings Represents the efficiency gains of using the MCP tool versus standard naive execution.
type Savings struct {
	BytesAvoided  int64 `json:"bytes_avoided"`
	TokensSaved   int64 `json:"tokens_saved"`
	EfficiencyPct int   `json:"efficiency_pct"`
}

// CalculateSavings returns the exact savings based on baseline vs actual metrics.
func CalculateSavings(baselineBytes, actualBytes int64) *Savings {
	if baselineBytes <= actualBytes {
		return nil
	}
	avoided := baselineBytes - actualBytes

	// Industry heuristical standard: 1 standard LLM token ~ 4 bytes of text/code
	tokensSaved := avoided / 4

	pct := 0
	if baselineBytes > 0 {
		pct = int((float64(avoided) / float64(baselineBytes)) * 100)
	}

	return &Savings{
		BytesAvoided:  avoided,
		TokensSaved:   tokensSaved,
		EfficiencyPct: pct,
	}
}
