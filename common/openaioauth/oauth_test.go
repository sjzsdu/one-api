package openaioauth

import (
	"net/url"
	"strings"
	"testing"
)

func TestDefaultOpenAIAuthorizeURLUsesCodexLoopbackRedirect(t *testing.T) {
	cfg := DefaultConfig()
	pkce := PKCECodes{
		CodeVerifier:  "test-verifier",
		CodeChallenge: "test-challenge",
	}
	redirectURI := LoopbackRedirectURI(cfg.CallbackPort)
	rawURL := BuildAuthorizeURL(cfg, pkce, "test-state", redirectURI)

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	query := parsed.Query()
	if got := query.Get("client_id"); got != cfg.ClientID {
		t.Fatalf("client_id = %q, want %q", got, cfg.ClientID)
	}
	if got := query.Get("redirect_uri"); got != "http://localhost:1455/auth/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	if got := query.Get("originator"); got != "codex_cli_rs" {
		t.Fatalf("originator = %q", got)
	}
	scope := query.Get("scope")
	for _, requiredScope := range []string{"openid", "profile", "email", "offline_access"} {
		if !strings.Contains(scope, requiredScope) {
			t.Fatalf("scope %q is missing %q", scope, requiredScope)
		}
	}
	if strings.Contains(scope, "api.model.images.request") {
		t.Fatalf("scope must not include api.model.images.request: %q", scope)
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q", got)
	}
}
