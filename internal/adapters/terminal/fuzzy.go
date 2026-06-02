package terminal

// FuzzyMatch checks if all characters in the search term appear in order
// (but not necessarily consecutively) in the target string.
// Both strings should be lowercase for case-insensitive matching.
//
// Examples:
//   - FuzzyMatch("zhipuglm5", "zhipu / glm-5") → true (all chars appear in order)
//   - FuzzyMatch("glm5", "zhipu / glm-5") → true (partial match)
//   - FuzzyMatch("glmzhipu", "zhipu / glm-5") → false (wrong order)
//   - FuzzyMatch("gt", ":taskqueue_get_all") → true
func FuzzyMatch(search, target string) bool {
	if search == "" {
		return true
	}
	if len(search) > len(target) {
		return false
	}

	searchIdx := 0
	for i := 0; i < len(target) && searchIdx < len(search); i++ {
		if search[searchIdx] == target[i] {
			searchIdx++
		}
	}
	return searchIdx == len(search)
}
