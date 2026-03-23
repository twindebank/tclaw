package telegramclient

import "tclaw/libraries/secret"

// NewSecretSessionStorageForTest creates a secretSessionStorage for testing.
func NewSecretSessionStorageForTest(store secret.Store) *secretSessionStorage {
	return newSecretSessionStorage(store)
}

// GenerateBotNamesForTest exposes generateBotNames for testing.
func GenerateBotNamesForTest(purpose string) (string, string, error) {
	return generateBotNames(purpose)
}

// ExtractTokenForTest exposes the tokenRegex for testing.
func ExtractTokenForTest(text string) string {
	return tokenRegex.FindString(text)
}

// ContainsErrorForTest exposes containsError for testing.
func ContainsErrorForTest(text string) bool {
	return containsError(text)
}
