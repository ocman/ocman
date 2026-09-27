package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const linkPreviewRulesKey = "link_preview_rules"

type linkPreviewRule struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

type linkPreviewRules struct {
	Rules []linkPreviewRule `json:"rules"`
}

func (s *Server) handleLinkPreviewRules(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		result := linkPreviewRules{Rules: []linkPreviewRule{}}
		value, ok, err := s.stateDB.GetSetting(r.Context(), linkPreviewRulesKey)
		if err != nil {
			serverError(w, "reading link preview rules", err)
			return
		}
		if ok {
			if err := json.Unmarshal([]byte(value), &result); err != nil {
				serverError(w, "decoding link preview rules", err)
				return
			}
		}
		writeJSON(w, result)
	case http.MethodPost:
		var result linkPreviewRules
		if !readAndUnmarshal(w, r, maxRequestBody, &result) {
			return
		}
		if err := validateLinkPreviewRules(result); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		value, err := json.Marshal(result)
		if err != nil {
			serverError(w, "encoding link preview rules", err)
			return
		}
		if err := s.stateDB.SetSetting(r.Context(), linkPreviewRulesKey, string(value)); err != nil {
			serverError(w, "saving link preview rules", err)
			return
		}
		writeJSON(w, result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func validateLinkPreviewRules(value linkPreviewRules) error {
	if value.Rules == nil || len(value.Rules) > 20 {
		return fmt.Errorf("rules must be an array of at most 20 entries")
	}
	for i, rule := range value.Rules {
		if len(rule.Pattern) == 0 || len(rule.Pattern) > 256 {
			return fmt.Errorf("rule %d: pattern must be 1-256 characters", i+1)
		}
		compiled, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return fmt.Errorf("rule %d: invalid regular expression: %w", i+1, err)
		}
		if compiled.MatchString("") {
			return fmt.Errorf("rule %d: pattern must not match empty text", i+1)
		}
		if len(rule.Replacement) > 2048 || !strings.Contains(rule.Replacement, "$&") && !strings.Contains(rule.Replacement, "$1") {
			return fmt.Errorf("rule %d: replacement must include $& or $1 and be at most 2048 characters", i+1)
		}
		if strings.Contains(rule.Replacement, "$1") && compiled.NumSubexp() == 0 {
			return fmt.Errorf("rule %d: $1 requires a capture group", i+1)
		}
		u, err := url.Parse(rule.Replacement)
		if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			return fmt.Errorf("rule %d: replacement must be an HTTP(S) URL", i+1)
		}
	}
	return nil
}
