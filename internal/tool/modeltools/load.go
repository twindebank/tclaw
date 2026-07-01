package modeltools

import (
	"context"
	"log/slog"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/store"
)

// LoadModel reads the model override from the store. Returns the config
// model if no override is set, or ModelAuto if both are empty.
func LoadModel(store store.Store, configModel claudecli.Model) claudecli.Model {
	if override := LoadOverride(store); override != "" {
		return override
	}
	return configModel
}

// LoadOverride reads the runtime model override from the store, returning
// ModelAuto (empty) when none is set. Unlike LoadModel it does not fall back to
// a config default — callers that want to layer their own fallback (e.g.
// per-channel models) use this to tell "explicitly set" from "unset".
func LoadOverride(store store.Store) claudecli.Model {
	raw, err := store.Get(context.Background(), storeKey)
	if err != nil {
		slog.Debug("no model override in store", "err", err)
		return claudecli.ModelAuto
	}
	return claudecli.Model(string(raw))
}
