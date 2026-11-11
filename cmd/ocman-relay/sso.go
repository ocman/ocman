package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/ocman/ocman/internal/relay"
	"github.com/ocman/ocman/internal/relayauth"
)

// Provider credentials come from environment references rather than a file
// containing secrets. An absent config preserves manual enrollment behavior.
func configureSSO(cfg *relay.Config) error {
	path := os.Getenv("OCMAN_RELAY_SSO_CONFIG")
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading SSO configuration: %w", err)
	}
	var providers []struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Kind            string `json:"kind"`
		ClientID        string `json:"clientId"`
		ClientSecretEnv string `json:"clientSecretEnv"`
	}
	if err := json.Unmarshal(data, &providers); err != nil {
		return errors.New("invalid SSO provider configuration")
	}
	choices := make([]relayauth.Provider, 0, len(providers))
	for _, p := range providers {
		choices = append(choices, relayauth.Provider{ID: p.ID, Name: p.Name, Kind: p.Kind, ClientID: p.ClientID, ClientSecret: os.Getenv(p.ClientSecretEnv)})
	}
	auth, err := relayauth.New(relayauth.Config{PublicURL: os.Getenv("OCMAN_RELAY_PUBLIC_URL"), Providers: choices, Store: cfg.Store, Now: cfg.Now, TrustProxy: cfg.TrustProxy})
	if err != nil {
		return err
	}
	cfg.EnrollmentAuth = auth
	return nil
}
