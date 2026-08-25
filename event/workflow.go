package event

import "strings"

// NormalizeWorkflowPath removes the ref suffix from GITHUB_WORKFLOW_REF while
// preserving the repository-relative workflow path.
func NormalizeWorkflowPath(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	lower := strings.ToLower(value)
	for _, marker := range []string{".yaml@", ".yml@"} {
		if separator := strings.Index(lower, marker); separator >= 0 {
			return value[:separator+len(marker)-1]
		}
	}
	return value
}
