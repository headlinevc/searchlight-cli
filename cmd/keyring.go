package cmd

import "github.com/headlinevc/searchlight-cli/internal/keyring"

func newKeyringBackend() keyring.Store {
	return keyring.New(globals.Cfg.CredentialsPath())
}
