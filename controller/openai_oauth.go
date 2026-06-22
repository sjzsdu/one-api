package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/openaioauth"
)

const (
	openAIOAuthMethodBrowser    = "browser"
	openAIOAuthMethodDeviceCode = "device_code"

	openAIOAuthPending = "pending"
	openAIOAuthSuccess = "success"
	openAIOAuthError   = "error"
	openAIOAuthExpired = "expired"
)

const (
	openAIOAuthBrowserTTL    = 10 * time.Minute
	openAIOAuthDeviceCodeTTL = 15 * time.Minute
	openAIOAuthTerminalTTL   = 30 * time.Minute
)

type openAIOAuthFlow struct {
	ID           string
	UserID       int
	Method       string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    time.Time
	Error        string
	CodeVerifier string
	OAuthState   string
	RedirectURI  string
	DeviceAuthID string
	UserCode     string
	VerifyURL    string
	Interval     int
	Credential   string
}

type openAIOAuthFlowResponse struct {
	FlowID     string `json:"flow_id"`
	Method     string `json:"method"`
	Status     string `json:"status"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	Error      string `json:"error,omitempty"`
	AuthURL    string `json:"auth_url,omitempty"`
	UserCode   string `json:"user_code,omitempty"`
	VerifyURL  string `json:"verify_url,omitempty"`
	Interval   int    `json:"interval,omitempty"`
	Credential string `json:"credential,omitempty"`
}

var (
	openAIOAuthMu     sync.Mutex
	openAIOAuthFlows  = make(map[string]*openAIOAuthFlow)
	openAIOAuthStates = make(map[string]string)
	openAIOAuthNow    = time.Now
)

func StartOpenAIOAuth(c *gin.Context) {
	var req struct {
		Method string `json:"method"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	method := strings.ToLower(strings.TrimSpace(req.Method))
	if method == "" {
		method = openAIOAuthMethodBrowser
	}
	userID := c.GetInt(ctxkey.Id)
	cfg := openaioauth.DefaultConfig()
	now := openAIOAuthNow()

	switch method {
	case openAIOAuthMethodBrowser:
		pkce, err := openaioauth.GeneratePKCE()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		state, err := openaioauth.GenerateState()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		redirectURI := openaioauth.LoopbackRedirectURI(cfg.CallbackPort)
		flow := &openAIOAuthFlow{
			ID:           newOpenAIOAuthFlowID(),
			UserID:       userID,
			Method:       method,
			Status:       openAIOAuthPending,
			CreatedAt:    now,
			UpdatedAt:    now,
			ExpiresAt:    now.Add(openAIOAuthBrowserTTL),
			CodeVerifier: pkce.CodeVerifier,
			OAuthState:   state,
			RedirectURI:  redirectURI,
		}
		storeOpenAIOAuthFlow(flow)
		if err := startOpenAIOAuthLoopbackCallback(flow.ID, state, cfg.CallbackPort); err != nil {
			deleteOpenAIOAuthFlow(flow.ID)
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		authURL := openaioauth.BuildAuthorizeURL(cfg, pkce, state, redirectURI)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": gin.H{
				"flow_id":    flow.ID,
				"method":     method,
				"status":     flow.Status,
				"auth_url":   authURL,
				"expires_at": flow.ExpiresAt.Format(time.RFC3339),
			},
		})
	case openAIOAuthMethodDeviceCode:
		info, err := openaioauth.RequestDeviceCode(cfg)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		flow := &openAIOAuthFlow{
			ID:           newOpenAIOAuthFlowID(),
			UserID:       userID,
			Method:       method,
			Status:       openAIOAuthPending,
			CreatedAt:    now,
			UpdatedAt:    now,
			ExpiresAt:    now.Add(openAIOAuthDeviceCodeTTL),
			DeviceAuthID: info.DeviceAuthID,
			UserCode:     info.UserCode,
			VerifyURL:    info.VerifyURL,
			Interval:     info.Interval,
		}
		storeOpenAIOAuthFlow(flow)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    openAIOAuthFlowToResponse(flow, false),
		})
	default:
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("unsupported OpenAI OAuth method: %s", method),
		})
	}
}

func startOpenAIOAuthLoopbackCallback(flowID, state string, port int) error {
	if port <= 0 {
		port = 1455
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("starting OpenAI OAuth callback server on localhost:%d: %w", port, err)
	}

	server := &http.Server{}
	shutdownOnce := sync.Once{}
	shutdown := func() {
		shutdownOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			go shutdown()
		}()

		flow, ok := getOpenAIOAuthFlow(flowID)
		if !ok {
			renderOpenAIOAuthLoopbackCallbackPage(w, openAIOAuthError, "OAuth flow not found", "flow_not_found")
			return
		}
		if r.URL.Query().Get("state") != state {
			setOpenAIOAuthFlowError(flow.ID, "state mismatch")
			renderOpenAIOAuthLoopbackCallbackPage(w, openAIOAuthError, "Authorization failed", "state mismatch")
			return
		}
		if errMsg := strings.TrimSpace(r.URL.Query().Get("error")); errMsg != "" {
			if desc := strings.TrimSpace(r.URL.Query().Get("error_description")); desc != "" {
				errMsg += ": " + desc
			}
			setOpenAIOAuthFlowError(flow.ID, errMsg)
			renderOpenAIOAuthLoopbackCallbackPage(w, openAIOAuthError, "Authorization failed", errMsg)
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" {
			setOpenAIOAuthFlowError(flow.ID, "missing authorization code")
			renderOpenAIOAuthLoopbackCallbackPage(w, openAIOAuthError, "Missing authorization code", "missing_code")
			return
		}

		cred, err := openaioauth.ExchangeCodeForTokens(openaioauth.DefaultConfig(), code, flow.CodeVerifier, flow.RedirectURI)
		if err != nil {
			setOpenAIOAuthFlowError(flow.ID, err.Error())
			renderOpenAIOAuthLoopbackCallbackPage(w, openAIOAuthError, "Token exchange failed", err.Error())
			return
		}
		credential, err := openaioauth.EncodeCredentialKey(cred)
		if err != nil {
			setOpenAIOAuthFlowError(flow.ID, err.Error())
			renderOpenAIOAuthLoopbackCallbackPage(w, openAIOAuthError, "Failed to save credential", err.Error())
			return
		}
		setOpenAIOAuthFlowSuccess(flow.ID, credential)
		renderOpenAIOAuthLoopbackCallbackPage(w, openAIOAuthSuccess, "Authentication successful", "")
	})
	server.Handler = mux

	go func() {
		timer := time.NewTimer(openAIOAuthBrowserTTL)
		defer timer.Stop()
		<-timer.C
		shutdown()
	}()
	go func() {
		_ = server.Serve(listener)
	}()
	return nil
}

func GetOpenAIOAuthFlow(c *gin.Context) {
	flow, ok := getOpenAIOAuthFlow(c.Param("id"))
	if !ok {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "OAuth flow not found",
		})
		return
	}
	if flow.UserID != c.GetInt(ctxkey.Id) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "OAuth flow not found",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    openAIOAuthFlowToResponse(flow, true),
	})
}

func PollOpenAIOAuthFlow(c *gin.Context) {
	flow, ok := getOpenAIOAuthFlow(c.Param("id"))
	if !ok {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "OAuth flow not found"})
		return
	}
	if flow.UserID != c.GetInt(ctxkey.Id) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "OAuth flow not found"})
		return
	}
	if flow.Method != openAIOAuthMethodDeviceCode {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "OAuth flow does not support polling"})
		return
	}
	if flow.Status != openAIOAuthPending {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": openAIOAuthFlowToResponse(flow, true)})
		return
	}

	cred, err := openaioauth.PollDeviceCodeOnce(openaioauth.DefaultConfig(), flow.DeviceAuthID, flow.UserCode)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "pending") {
			c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": openAIOAuthFlowToResponse(flow, false)})
			return
		}
		setOpenAIOAuthFlowError(flow.ID, err.Error())
		updated, _ := getOpenAIOAuthFlow(flow.ID)
		if updated == nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "OAuth flow not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": openAIOAuthFlowToResponse(updated, true)})
		return
	}
	credential, err := openaioauth.EncodeCredentialKey(cred)
	if err != nil {
		setOpenAIOAuthFlowError(flow.ID, err.Error())
		updated, _ := getOpenAIOAuthFlow(flow.ID)
		if updated == nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "OAuth flow not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": openAIOAuthFlowToResponse(updated, true)})
		return
	}
	setOpenAIOAuthFlowSuccess(flow.ID, credential)
	updated, _ := getOpenAIOAuthFlow(flow.ID)
	if updated == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "OAuth flow not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": openAIOAuthFlowToResponse(updated, true)})
}

func OpenAIOAuthCallback(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		renderOpenAIOAuthCallbackPage(c, "", openAIOAuthError, "Missing state", "missing_state")
		return
	}
	flow, ok := getOpenAIOAuthFlowByState(state)
	if !ok {
		renderOpenAIOAuthCallbackPage(c, "", openAIOAuthError, "OAuth flow not found", "flow_not_found")
		return
	}
	if flow.Status != openAIOAuthPending {
		renderOpenAIOAuthCallbackPage(c, flow.ID, flow.Status, "Flow already completed", flow.Error)
		return
	}
	if errMsg := strings.TrimSpace(c.Query("error")); errMsg != "" {
		if desc := strings.TrimSpace(c.Query("error_description")); desc != "" {
			errMsg += ": " + desc
		}
		setOpenAIOAuthFlowError(flow.ID, errMsg)
		renderOpenAIOAuthCallbackPage(c, flow.ID, openAIOAuthError, "Authorization failed", errMsg)
		return
	}
	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		setOpenAIOAuthFlowError(flow.ID, "missing authorization code")
		renderOpenAIOAuthCallbackPage(c, flow.ID, openAIOAuthError, "Missing authorization code", "missing_code")
		return
	}

	cred, err := openaioauth.ExchangeCodeForTokens(openaioauth.DefaultConfig(), code, flow.CodeVerifier, flow.RedirectURI)
	if err != nil {
		setOpenAIOAuthFlowError(flow.ID, err.Error())
		renderOpenAIOAuthCallbackPage(c, flow.ID, openAIOAuthError, "Token exchange failed", err.Error())
		return
	}
	credential, err := openaioauth.EncodeCredentialKey(cred)
	if err != nil {
		setOpenAIOAuthFlowError(flow.ID, err.Error())
		renderOpenAIOAuthCallbackPage(c, flow.ID, openAIOAuthError, "Failed to save credential", err.Error())
		return
	}
	setOpenAIOAuthFlowSuccess(flow.ID, credential)
	renderOpenAIOAuthCallbackPage(c, flow.ID, openAIOAuthSuccess, "Authentication successful", "")
}

func buildOpenAIOAuthRedirectURI(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	host := r.Host
	if forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
		host = strings.TrimSpace(strings.Split(forwardedHost, ",")[0])
	}
	return fmt.Sprintf("%s://%s/api/oauth/openai/callback", scheme, host)
}

func renderOpenAIOAuthCallbackPage(c *gin.Context, flowID, status, title, errMsg string) {
	payload := map[string]string{
		"type":   "one-api-openai-oauth-result",
		"flowId": flowID,
		"status": status,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	payloadJSON, _ := json.Marshal(payload)
	message := title
	if errMsg != "" {
		message = fmt.Sprintf("%s: %s", title, errMsg)
	}
	statusCode := http.StatusBadRequest
	if status == openAIOAuthSuccess {
		statusCode = http.StatusOK
	}
	c.Data(
		statusCode,
		"text/html; charset=utf-8",
		[]byte(fmt.Sprintf(
			"<!doctype html><html><head><meta charset=\"utf-8\"><title>OpenAI OAuth</title></head><body><script>(function(){var payload=%s;try{if(window.opener&&!window.opener.closed){window.opener.postMessage(payload,window.location.origin);window.close();return}}catch(e){}setTimeout(function(){window.close()},1200)})();</script><div style=\"font-family:Inter,system-ui,sans-serif;padding:24px\"><h2>%s</h2><p>%s</p><p>You can close this window.</p></div></body></html>",
			string(payloadJSON),
			html.EscapeString(title),
			html.EscapeString(message),
		)),
	)
}

func renderOpenAIOAuthLoopbackCallbackPage(w http.ResponseWriter, status, title, errMsg string) {
	message := title
	if errMsg != "" {
		message = fmt.Sprintf("%s: %s", title, errMsg)
	}
	statusCode := http.StatusBadRequest
	if status == openAIOAuthSuccess {
		statusCode = http.StatusOK
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(
		w,
		"<!doctype html><html><head><meta charset=\"utf-8\"><title>OpenAI OAuth</title></head><body><script>setTimeout(function(){window.close()},1200)</script><div style=\"font-family:Inter,system-ui,sans-serif;padding:24px\"><h2>%s</h2><p>%s</p><p>You can close this window.</p></div></body></html>",
		html.EscapeString(title),
		html.EscapeString(message),
	)
}

func openAIOAuthFlowToResponse(flow *openAIOAuthFlow, includeCredential bool) openAIOAuthFlowResponse {
	resp := openAIOAuthFlowResponse{
		FlowID:    flow.ID,
		Method:    flow.Method,
		Status:    flow.Status,
		Error:     flow.Error,
		UserCode:  flow.UserCode,
		VerifyURL: flow.VerifyURL,
		Interval:  flow.Interval,
	}
	if !flow.ExpiresAt.IsZero() {
		resp.ExpiresAt = flow.ExpiresAt.Format(time.RFC3339)
	}
	if includeCredential && flow.Status == openAIOAuthSuccess {
		resp.Credential = flow.Credential
	}
	return resp
}

func newOpenAIOAuthFlowID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("oauth_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func storeOpenAIOAuthFlow(flow *openAIOAuthFlow) {
	now := openAIOAuthNow()
	openAIOAuthMu.Lock()
	defer openAIOAuthMu.Unlock()
	gcOpenAIOAuthFlowsLocked(now)
	openAIOAuthFlows[flow.ID] = flow
	if flow.OAuthState != "" {
		openAIOAuthStates[flow.OAuthState] = flow.ID
	}
}

func deleteOpenAIOAuthFlow(flowID string) {
	openAIOAuthMu.Lock()
	defer openAIOAuthMu.Unlock()
	if flow, ok := openAIOAuthFlows[flowID]; ok && flow.OAuthState != "" {
		delete(openAIOAuthStates, flow.OAuthState)
	}
	delete(openAIOAuthFlows, flowID)
}

func getOpenAIOAuthFlow(flowID string) (*openAIOAuthFlow, bool) {
	now := openAIOAuthNow()
	openAIOAuthMu.Lock()
	defer openAIOAuthMu.Unlock()
	gcOpenAIOAuthFlowsLocked(now)
	flow, ok := openAIOAuthFlows[flowID]
	if !ok {
		return nil, false
	}
	cp := *flow
	return &cp, true
}

func getOpenAIOAuthFlowByState(state string) (*openAIOAuthFlow, bool) {
	now := openAIOAuthNow()
	openAIOAuthMu.Lock()
	defer openAIOAuthMu.Unlock()
	gcOpenAIOAuthFlowsLocked(now)
	flowID, ok := openAIOAuthStates[state]
	if !ok {
		return nil, false
	}
	flow, ok := openAIOAuthFlows[flowID]
	if !ok {
		delete(openAIOAuthStates, state)
		return nil, false
	}
	cp := *flow
	return &cp, true
}

func setOpenAIOAuthFlowSuccess(flowID string, credential string) {
	now := openAIOAuthNow()
	openAIOAuthMu.Lock()
	defer openAIOAuthMu.Unlock()
	flow, ok := openAIOAuthFlows[flowID]
	if !ok {
		return
	}
	flow.Status = openAIOAuthSuccess
	flow.Error = ""
	flow.Credential = credential
	flow.UpdatedAt = now
	if flow.OAuthState != "" {
		delete(openAIOAuthStates, flow.OAuthState)
	}
}

func setOpenAIOAuthFlowError(flowID, errMsg string) {
	now := openAIOAuthNow()
	openAIOAuthMu.Lock()
	defer openAIOAuthMu.Unlock()
	flow, ok := openAIOAuthFlows[flowID]
	if !ok {
		return
	}
	flow.Status = openAIOAuthError
	flow.Error = errMsg
	flow.UpdatedAt = now
	if flow.OAuthState != "" {
		delete(openAIOAuthStates, flow.OAuthState)
	}
}

func gcOpenAIOAuthFlowsLocked(now time.Time) {
	for id, flow := range openAIOAuthFlows {
		if flow.Status == openAIOAuthPending && !flow.ExpiresAt.IsZero() && now.After(flow.ExpiresAt) {
			flow.Status = openAIOAuthExpired
			flow.Error = "flow expired"
			flow.UpdatedAt = now
			if flow.OAuthState != "" {
				delete(openAIOAuthStates, flow.OAuthState)
			}
		}
		if flow.Status != openAIOAuthPending && now.Sub(flow.UpdatedAt) > openAIOAuthTerminalTTL {
			if flow.OAuthState != "" {
				delete(openAIOAuthStates, flow.OAuthState)
			}
			delete(openAIOAuthFlows, id)
		}
	}
}
