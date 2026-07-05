package service

// SourceBranch returns the configured service branch or fibe-distilled's default.
func SourceBranch(summary Summary) string {
	if summary.Branch != "" {
		return summary.Branch
	}
	return "main"
}
