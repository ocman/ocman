package relayauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type endpoints struct{ authorize, token, identity string }

func defaultEndpoints() map[string]endpoints {
	return map[string]endpoints{
		"github": {"https://github.com/login/oauth/authorize", "https://github.com/login/oauth/access_token", "https://api.github.com/user"},
		"google": {"https://accounts.google.com/o/oauth2/v2/auth", "https://oauth2.googleapis.com/token", "https://openidconnect.googleapis.com/v1/userinfo"},
	}
}

func (s *Service) callbackURL(p Provider) string { return s.cfg.PublicURL + "/auth/callback/" + p.ID }

func (s *Service) identity(ctx context.Context, p Provider, verifier, code string) (string, error) {
	e := s.endpoints[p.Kind]
	form := url.Values{"client_id": {p.ClientID}, "client_secret": {p.ClientSecret}, "code": {code}, "redirect_uri": {s.callbackURL(p)}, "grant_type": {"authorization_code"}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := s.request(req, &token); err != nil {
		return "", err
	}
	if token.AccessToken == "" {
		return "", errors.New("provider returned no access token")
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, e.identity, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	var identity struct {
		ID      json.Number `json:"id"`
		Subject string      `json:"sub"`
	}
	if err := s.request(req, &identity); err != nil {
		return "", err
	}
	if p.Kind == "github" && identity.ID != "" {
		return string(identity.ID), nil
	}
	if p.Kind == "google" && identity.Subject != "" {
		return identity.Subject, nil
	}
	return "", errors.New("provider returned no stable user identity")
}

func (s *Service) request(req *http.Request, target any) error {
	req.Header.Set("Accept", "application/json")
	// Never forward provider credentials to a redirect destination.
	client := *s.cfg.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return errors.New("identity provider request failed")
	}
	return json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(target)
}
