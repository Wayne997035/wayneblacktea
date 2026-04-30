package handler

// Exported wrappers for internal functions — only compiled in test builds.

// BuildAuthTokenForTest exposes buildAuthToken for unit testing.
func BuildAuthTokenForTest(apiKey, ts string) string {
	return buildAuthToken(apiKey, ts)
}

// ValidateAuthTokenForTest exposes validateAuthToken for unit testing.
func ValidateAuthTokenForTest(apiKey, token string) bool {
	return validateAuthToken(apiKey, token)
}
