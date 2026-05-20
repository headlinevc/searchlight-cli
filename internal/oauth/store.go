package oauth

import (
	"encoding/json"
	"errors"

	"github.com/headlinevc/searchlight-cli/internal/keyring"
)

const credKey = "tokens.v1"

// KeyringStore persists Tokens as a single JSON blob in the OS keyring (or the
// file fallback in keyring.OSStore). One blob keeps access+refresh atomically
// in sync and avoids partial-rotation states.
type KeyringStore struct {
	Backend keyring.Store
}

func (s KeyringStore) Load() (*Tokens, error) {
	raw, err := s.Backend.Get(credKey)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var t Tokens
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s KeyringStore) Save(t *Tokens) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return s.Backend.Set(credKey, string(data))
}

func (s KeyringStore) Clear() error {
	return s.Backend.Delete(credKey)
}
