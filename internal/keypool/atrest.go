package keypool

import (
	"context"
	"fmt"

	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
)

type AtRestStore interface {
	GetAllKeys(ctx context.Context) ([]*db.Key, error)
	KeySecretsByKey(ctx context.Context) (map[string][]*db.KeySecret, error)
	ReplaceKeyValue(ctx context.Context, keyID, value string) error
	ReplaceKeySecretValue(ctx context.Context, keyID, name, value string) error
}

func SealAtRest(ctx context.Context, store AtRestStore, sealer *crypto.Sealer) (int, error) {
	if !sealer.Enabled() {
		return 0, nil
	}
	keys, err := store.GetAllKeys(ctx)
	if err != nil {
		return 0, fmt.Errorf("list keys: %w", err)
	}
	sealed := 0
	for _, k := range keys {
		if crypto.IsSealed(k.KeyValue) {
			continue
		}
		value, err := sealer.Seal(k.KeyValue)
		if err != nil {
			return sealed, fmt.Errorf("seal key %s: %w", k.ID, err)
		}
		if err := store.ReplaceKeyValue(ctx, k.ID, value); err != nil {
			return sealed, fmt.Errorf("store key %s: %w", k.ID, err)
		}
		sealed++
	}
	bound, err := store.KeySecretsByKey(ctx)
	if err != nil {
		return sealed, fmt.Errorf("list secrets: %w", err)
	}
	for _, secrets := range bound {
		for _, s := range secrets {
			if crypto.IsSealed(s.Value) {
				continue
			}
			value, err := sealer.Seal(s.Value)
			if err != nil {
				return sealed, fmt.Errorf("seal secret %s/%s: %w", s.KeyID, s.Name, err)
			}
			if err := store.ReplaceKeySecretValue(ctx, s.KeyID, s.Name, value); err != nil {
				return sealed, fmt.Errorf("store secret %s/%s: %w", s.KeyID, s.Name, err)
			}
			sealed++
		}
	}
	return sealed, nil
}
