package secretform

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"tclaw/internal/libraries/store"
)

// collectedKeysStoreKey holds the flat secret keys a user filled in through a
// form. A list of names, not values, so it lives in the state store.
//
// Recording what the user gave us is exact. Listing what they did not would mean
// enumerating every key the tool packages write, and being wrong the first time
// one of them writes another.
const collectedKeysStoreKey = "agent_collected_secret_keys"

// CollectedKeys are the secrets a user handed over through a form, and so the
// only ones the agent may ask to have deleted.
func CollectedKeys(ctx context.Context, st store.Store) (map[string]bool, error) {
	names, err := readCollectedKeys(ctx, st)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(names))
	for _, name := range names {
		keys[name] = true
	}
	return keys, nil
}

// recordCollectedKey notes that the user supplied this key, so it can later be
// deleted on their word.
func recordCollectedKey(ctx context.Context, st store.Store, key string) error {
	if st == nil {
		return fmt.Errorf("no state store, so %q cannot be recorded as user-supplied", key)
	}
	names, err := readCollectedKeys(ctx, st)
	if err != nil {
		return err
	}
	if slices.Contains(names, key) {
		return nil
	}
	return writeCollectedKeys(ctx, st, append(names, key))
}

// ForgetCollectedKey drops a key from the list once its secret is gone, so the
// list does not grow with names that no longer exist.
func ForgetCollectedKey(ctx context.Context, st store.Store, key string) error {
	names, err := readCollectedKeys(ctx, st)
	if err != nil {
		return err
	}
	remaining := slices.DeleteFunc(names, func(name string) bool { return name == key })
	if len(remaining) == len(names) {
		return nil
	}
	return writeCollectedKeys(ctx, st, remaining)
}

func readCollectedKeys(ctx context.Context, st store.Store) ([]string, error) {
	data, err := st.Get(ctx, collectedKeysStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read collected secret keys: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("parse collected secret keys: %w", err)
	}
	return names, nil
}

func writeCollectedKeys(ctx context.Context, st store.Store, names []string) error {
	data, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("marshal collected secret keys: %w", err)
	}
	if err := st.Set(ctx, collectedKeysStoreKey, data); err != nil {
		return fmt.Errorf("save collected secret keys: %w", err)
	}
	return nil
}
