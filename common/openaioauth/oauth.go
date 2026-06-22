package openaioauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Issuer       string
	ClientID     string
	Scopes       string
	Originator   string
	CallbackPort int
}

type PKCECodes struct {
	CodeVerifier  string
	CodeChallenge string
}

type Credential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	AuthMethod   string    `json:"auth_method,omitempty"`
}

type DeviceCodeInfo struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	VerifyURL    string `json:"verify_url"`
	Interval     int    `json:"interval"`
}

func DefaultConfig() Config {
	return Config{
		Issuer:       "https://auth.openai.com",
		ClientID:     "app_EMoamEEZ73f0CkXaXp7hrann",
		Scopes:       "openid profile email offline_access",
		Originator:   "codex_cli_rs",
		CallbackPort: 1455,
	}
}

func GenerateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func GeneratePKCE() (PKCECodes, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return PKCECodes{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return PKCECodes{CodeVerifier: verifier, CodeChallenge: challenge}, nil
}

func BuildAuthorizeURL(cfg Config, pkce PKCECodes, state, redirectURI string) string {
	params := url.Values{
		"response_type":              {"code"},
		"client_id":                  {cfg.ClientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {cfg.Scopes},
		"code_challenge":             {pkce.CodeChallenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
	}
	if cfg.Originator != "" {
		params.Set("originator", cfg.Originator)
	}
	return strings.TrimRight(cfg.Issuer, "/") + "/oauth/authorize?" + params.Encode()
}

func LoopbackRedirectURI(port int) string {
	if port <= 0 {
		port = 1455
	}
	return fmt.Sprintf("http://localhost:%d/auth/callback", port)
}

func RequestDeviceCode(cfg Config) (*DeviceCodeInfo, error) {
	reqBody, _ := json.Marshal(map[string]string{"client_id": cfg.ClientID})
	resp, err := http.Post(
		strings.TrimRight(cfg.Issuer, "/")+"/api/accounts/deviceauth/usercode",
		"application/json",
		strings.NewReader(string(reqBody)),
	)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading device code response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed: %s", string(body))
	}

	var raw struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}
	interval, err := parseFlexibleInt(raw.Interval)
	if err != nil {
		return nil, fmt.Errorf("parsing device code interval: %w", err)
	}
	if interval < 1 {
		interval = 5
	}

	return &DeviceCodeInfo{
		DeviceAuthID: raw.DeviceAuthID,
		UserCode:     raw.UserCode,
		VerifyURL:    strings.TrimRight(cfg.Issuer, "/") + "/codex/device",
		Interval:     interval,
	}, nil
}

func PollDeviceCodeOnce(cfg Config, deviceAuthID, userCode string) (*Credential, error) {
	reqBody, _ := json.Marshal(map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	})

	resp, err := http.Post(
		strings.TrimRight(cfg.Issuer, "/")+"/api/accounts/deviceauth/token",
		"application/json",
		strings.NewReader(string(reqBody)),
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pending")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading device token response: %w", err)
	}

	var tokenResp struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}
	if tokenResp.AuthorizationCode == "" || tokenResp.CodeVerifier == "" {
		return nil, fmt.Errorf("device token response missing authorization code or verifier")
	}

	redirectURI := strings.TrimRight(cfg.Issuer, "/") + "/deviceauth/callback"
	return ExchangeCodeForTokens(cfg, tokenResp.AuthorizationCode, tokenResp.CodeVerifier, redirectURI)
}

func ExchangeCodeForTokens(cfg Config, code, codeVerifier, redirectURI string) (*Credential, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {codeVerifier},
	}
	resp, err := http.PostForm(strings.TrimRight(cfg.Issuer, "/")+"/oauth/token", data)
	if err != nil {
		return nil, fmt.Errorf("exchanging code for tokens: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token exchange response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	return parseTokenResponse(body)
}

func RefreshAccessToken(cred *Credential, cfg Config) (*Credential, error) {
	if cred == nil || cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}
	data := url.Values{
		"client_id":     {cfg.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {cred.RefreshToken},
		"scope":         {"openid profile email"},
	}
	resp, err := http.PostForm(strings.TrimRight(cfg.Issuer, "/")+"/oauth/token", data)
	if err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %s", string(body))
	}

	refreshed, err := parseTokenResponse(body)
	if err != nil {
		return nil, err
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = cred.RefreshToken
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = cred.AccountID
	}
	return refreshed, nil
}

func ParseCredentialKey(key string) (*Credential, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("empty OpenAI OAuth credential")
	}
	if !strings.HasPrefix(key, "{") {
		return &Credential{
			AccessToken: key,
			AuthMethod:  "token",
		}, nil
	}
	var cred Credential
	if err := json.Unmarshal([]byte(key), &cred); err != nil {
		return nil, err
	}
	if cred.AccessToken == "" {
		return nil, fmt.Errorf("OpenAI OAuth credential is missing access_token")
	}
	if cred.AuthMethod == "" {
		cred.AuthMethod = "oauth"
	}
	return &cred, nil
}

func EncodeCredentialKey(cred *Credential) (string, error) {
	if cred == nil {
		return "", fmt.Errorf("empty OpenAI OAuth credential")
	}
	cp := *cred
	if cp.AuthMethod == "" {
		cp.AuthMethod = "oauth"
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Credential) NeedsRefresh() bool {
	if c == nil || c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(c.ExpiresAt)
}

func parseTokenResponse(body []byte) (*Credential, error) {
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response")
	}

	cred := &Credential{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		AuthMethod:   "oauth",
	}
	if tokenResp.ExpiresIn > 0 {
		cred.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	if accountID := extractAccountID(tokenResp.IDToken); accountID != "" {
		cred.AccountID = accountID
	} else if accountID := extractAccountID(tokenResp.AccessToken); accountID != "" {
		cred.AccountID = accountID
	}
	return cred, nil
}

func parseFlexibleInt(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return 0, nil
		}
		return strconv.Atoi(text)
	}
	return 0, fmt.Errorf("invalid integer value: %s", string(raw))
}

func extractAccountID(token string) string {
	claims, err := parseJWTClaims(token)
	if err != nil {
		return ""
	}
	if accountID, ok := claims["chatgpt_account_id"].(string); ok && accountID != "" {
		return accountID
	}
	if accountID, ok := claims["https://api.openai.com/auth.chatgpt_account_id"].(string); ok && accountID != "" {
		return accountID
	}
	if authClaim, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if accountID, ok := authClaim["chatgpt_account_id"].(string); ok && accountID != "" {
			return accountID
		}
	}
	if orgs, ok := claims["organizations"].([]any); ok {
		for _, org := range orgs {
			orgMap, ok := org.(map[string]any)
			if !ok {
				continue
			}
			if accountID, ok := orgMap["id"].(string); ok && accountID != "" {
				return accountID
			}
		}
	}
	return ""
}

func parseJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("token is not a JWT")
	}
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}
