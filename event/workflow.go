package event

import "strings"

// NormalizeWorkflowPath removes the ref suffix from GITHUB_WORKFLOW_REF while
// preserving the repository-relative workflow path.
func NormalizeWorkflowPath(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	lower := strings.ToLower(value)
	separator := -1
	markerLength := 0
	for _, marker := range []string{".yaml@", ".yml@"} {
		if index := strings.LastIndex(lower, marker); index > separator {
			separator = index
			markerLength = len(marker)
		}
	}
	if separator >= 0 {
		return value[:separator+markerLength-1]
	}
	return value
}
