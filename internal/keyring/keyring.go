package keyring

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	kr "github.com/zalando/go-keyring"
)

const serviceName = "searchlight-cli"

var ErrNotFound = errors.New("credential not found")

type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// OSStore wraps the OS keyring (macOS Keychain, Windows Credential Manager, libsecret).
// Falls back to a 0600 file at credentialsPath when the OS keyring is unavailable
// (headless Linux, containers, CI).
type OSStore struct {
	credentialsPath string
}

func New(credentialsPath string) *OSStore {
	return &OSStore{credentialsPath: credentialsPath}
}

func (s *OSStore) Get(key string) (string, error) {
	v, err := kr.Get(serviceName, key)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, kr.ErrNotFound) {
		// Likely no OS keyring → fall through to file
		return s.fileGet(key)
	}
	// keyring is present but value is missing — still check file in case the
	// user previously ran without a keyring available
	if v, ferr := s.fileGet(key); ferr == nil {
		return v, nil
	}
	return "", ErrNotFound
}

func (s *OSStore) Set(key, value string) error {
	if err := kr.Set(serviceName, key, value); err == nil {
		return nil
	}
	return s.fileSet(key, value)
}

func (s *OSStore) Delete(key string) error {
	_ = kr.Delete(serviceName, key)
	return s.fileDelete(key)
}

func (s *OSStore) readFile() (map[string]string, error) {
	data, err := os.ReadFile(s.credentialsPath)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if len(data) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse credentials file: %w", err)
	}
	return out, nil
}

func (s *OSStore) writeFile(creds map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.credentialsPath), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return os.WriteFile(s.credentialsPath, data, 0o600)
}

func (s *OSStore) fileGet(key string) (string, error) {
	creds, err := s.readFile()
	if err != nil {
		return "", err
	}
	v, ok := creds[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *OSStore) fileSet(key, value string) error {
	creds, err := s.readFile()
	if err != nil {
		return err
	}
	creds[key] = value
	return s.writeFile(creds)
}

func (s *OSStore) fileDelete(key string) error {
	creds, err := s.readFile()
	if err != nil {
		return err
	}
	delete(creds, key)
	if len(creds) == 0 {
		_ = os.Remove(s.credentialsPath)
		return nil
	}
	return s.writeFile(creds)
}
