package discovery

// matchesPattern returns true if value equals pattern, or pattern is "*".
func matchesPattern(pattern, value string) bool {
	return pattern == "*" || pattern == value
}
