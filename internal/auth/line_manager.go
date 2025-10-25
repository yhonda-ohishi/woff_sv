package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// LINEConfig holds LINE Login configuration
type LINEConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       []string // "profile", "openid", "email"
}

// LINEManager handles LINE Login OAuth 2.0 operations
type LINEManager struct {
	config      *LINEConfig
	httpClient  *http.Client
	stateStore  map[string]time.Time // CSRF state storage
	stateMutex  sync.RWMutex
	authBaseURL string
	tokenURL    string
	userInfoURL string
}

// LINETokenResponse represents LINE token endpoint response
type LINETokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

// LINEUserInfo represents LINE user profile
type LINEUserInfo struct {
	UserID         string `json:"userId"`
	DisplayName    string `json:"displayName"`
	PictureURL     string `json:"pictureUrl"`
	StatusMessage  string `json:"statusMessage"`
	Email          string `json:"email"`
	EmailVerified  bool   `json:"emailVerified"`
}

// NewLINEManager creates a new LINE Login manager
func NewLINEManager(config *LINEConfig) *LINEManager {
	if len(config.Scopes) == 0 {
		config.Scopes = []string{"profile", "openid"}
	}

	return &LINEManager{
		config:      config,
		httpClient:  &http.Client{},
		stateStore:  make(map[string]time.Time),
		authBaseURL: "https://access.line.me/oauth2/v2.1/authorize",
		tokenURL:    "https://api.line.me/oauth2/v2.1/token",
		userInfoURL: "https://api.line.me/v2/profile",
	}
}

// generateState generates a random state string for CSRF protection
func (m *LINEManager) generateState() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// storeState stores a state value with expiration
func (m *LINEManager) storeState(state string) {
	m.stateMutex.Lock()
	m.stateStore[state] = time.Now().Add(10 * time.Minute)
	m.stateMutex.Unlock()
}

// GenerateAuthorizationURL generates LINE OAuth authorization URL
func (m *LINEManager) GenerateAuthorizationURL(redirectURI, state string, scopes []string) (string, string, error) {
	if state == "" {
		state = m.generateState()
	}
	m.storeState(state)

	// Use provided scopes or default
	scopeList := scopes
	if len(scopeList) == 0 {
		scopeList = m.config.Scopes
	}

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", m.config.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	params.Set("scope", strings.Join(scopeList, " "))

	authURL := fmt.Sprintf("%s?%s", m.authBaseURL, params.Encode())
	return authURL, state, nil
}

// VerifyState verifies the state parameter for CSRF protection
func (m *LINEManager) VerifyState(state string) bool {
	m.stateMutex.RLock()
	expiry, exists := m.stateStore[state]
	m.stateMutex.RUnlock()

	if !exists {
		return false
	}

	// Check if state is expired
	if time.Now().After(expiry) {
		m.stateMutex.Lock()
		delete(m.stateStore, state)
		m.stateMutex.Unlock()
		return false
	}

	// State is valid, delete it (one-time use)
	m.stateMutex.Lock()
	delete(m.stateStore, state)
	m.stateMutex.Unlock()

	return true
}

// ExchangeCode exchanges authorization code for access token
func (m *LINEManager) ExchangeCode(ctx context.Context, code, redirectURI string) (*LINETokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", m.config.ClientID)
	data.Set("client_secret", m.config.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", m.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s - %s", resp.Status, string(body))
	}

	var tokenResp LINETokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}

// GetUserInfo retrieves user profile from LINE
func (m *LINEManager) GetUserInfo(ctx context.Context, accessToken string) (*LINEUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", m.userInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get user info failed: %s - %s", resp.Status, string(body))
	}

	var userInfo LINEUserInfo
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return nil, fmt.Errorf("failed to parse user info: %w", err)
	}

	return &userInfo, nil
}

// RefreshToken refreshes an access token using refresh token
func (m *LINEManager) RefreshToken(ctx context.Context, refreshToken string) (*LINETokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", m.config.ClientID)
	data.Set("client_secret", m.config.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", m.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %s - %s", resp.Status, string(body))
	}

	var tokenResp LINETokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}
