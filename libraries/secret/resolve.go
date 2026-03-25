package secret

import (
	"fmt"
	"log/slog"
)

// MasterKeyEnv is the env var for the master encryption key,
// required when no OS keychain is available (e.g. containers).
const MasterKeyEnv = "TCLAW_SECRET_KEY"

// Resolve returns the best available secret store for a user.
//
// Priority:
//  1. OS keychain (macOS Keychain, Linux Secret Service) — used locally
//  2. Encrypted filesystem — used in containers/CI where no keychain exists,
//     requires TCLAW_SECRET_KEY env var for the master encryption key
//
// storeDir is the directory for encrypted files (only used if keychain
// is unavailable). masterKey may be empty if keychain is available.
func Resolve(userID string, storeDir string, masterKey string) (Store, error) {
	if KeychainAvailable() {
		slog.Debug("using OS keychain for secrets", "user", userID)
		return NewKeychainStore(userID), nil
	}

	if masterKey == "" {
		return nil, fmt.Errorf("no OS keychain available and %s not set — "+
			"set %s env var to enable encrypted secret storage", MasterKeyEnv, MasterKeyEnv)
	}

	// Enforce minimum key length to prevent weak master keys from being
	// used to derive encryption keys (HKDF doesn't compensate for low entropy).
	const minKeyLength = 32
	if len(masterKey) < minKeyLength {
		return nil, fmt.Errorf("%s must be at least %d characters (got %d)", MasterKeyEnv, minKeyLength, len(masterKey))
	}

	slog.Debug("using encrypted filesystem for secrets", "user", userID, "dir", storeDir)
	return NewEncryptedFSStore(storeDir, []byte(masterKey), userID)
}
