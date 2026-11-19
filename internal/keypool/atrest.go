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

// SealAtRest leaves every key and bound secret sealed under the current key: plain values are
// sealed, and values sealed under the previous key during a rotation are re-sealed. A value no
// configured key opens stops it, so a lost key fails the boot instead of losing the value.
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
		value, changed, err := underCurrentKey(sealer, k.KeyValue)
		if err != nil {
			return sealed, fmt.Errorf("seal key %s: %w", k.ID, err)
		}
		if !changed {
			continue
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
			value, changed, err := underCurrentKey(sealer, s.Value)
			if err != nil {
				return sealed, fmt.Errorf("seal secret %s/%s: %w", s.KeyID, s.Name, err)
			}
			if !changed {
				continue
			}
			if err := store.ReplaceKeySecretValue(ctx, s.KeyID, s.Name, value); err != nil {
				return sealed, fmt.Errorf("store secret %s/%s: %w", s.KeyID, s.Name, err)
			}
			sealed++
		}
	}
	return sealed, nil
}

func underCurrentKey(sealer *crypto.Sealer, stored string) (string, bool, error) {
	if sealer.Current(stored) {
		return stored, false, nil
	}
	plain, err := sealer.Open(stored)
	if err != nil {
		return "", false, err
	}
	value, err := sealer.Seal(plain)
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}
