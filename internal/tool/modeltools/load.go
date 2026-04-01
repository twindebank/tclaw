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
	raw, err := store.Get(context.Background(), storeKey)
	if err != nil {
		slog.Debug("no model override in store", "err", err)
		return configModel
	}
	override := claudecli.Model(string(raw))
	if override == "" {
		return configModel
	}
	return override
}
