package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidWOFFToken = errors.New("invalid WOFF token")
	ErrWOFFExpiredToken = errors.New("WOFF token has expired")
	ErrWOFFAPIError     = errors.New("WOFF API error")
)

// WOFF API endpoints
const (
	WOFFAuthURL     = "https://auth.worksmobile.com/oauth2/v2.0/authorize"
	WOFFTokenURL    = "https://auth.worksmobile.com/oauth2/v2.0/token"
	WOFFUserInfoURL = "https://www.worksapis.com/v1.0/users/me"
)

// WOFFConfig holds WOFF OAuth configuration
type WOFFConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       []string
}

// WOFFTokenResponse represents the token response from WOFF
type WOFFTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in,string"` // LINE WORKS returns this as a string
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"` // ID Token (openid scope required)
}

// FlexibleString can unmarshal from either a string or a number
type FlexibleString string

func (fs *FlexibleString) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*fs = FlexibleString(s)
		return nil
	}

	// Try to unmarshal as number
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*fs = FlexibleString(n.String())
		return nil
	}

	return fmt.Errorf("value must be string or number")
}

// WOFFUserName represents the user name structure from LINE WORKS
type WOFFUserName struct {
	LastName          string `json:"lastName"`
	FirstName         string `json:"firstName"`
	PhoneticLastName  string `json:"phoneticLastName"`
	PhoneticFirstName string `json:"phoneticFirstName"`
}

// FullName returns the full name
func (n WOFFUserName) FullName() string {
	return n.LastName + " " + n.FirstName
}

// WOFFUserInfo represents user information from WOFF
type WOFFUserInfo struct {
	UserID          string          `json:"userId"`
	UserName        WOFFUserName    `json:"userName"`
	NickName        string          `json:"nickName"`
	Email           string          `json:"email"`
	PrivateEmail    string          `json:"privateEmail"`
	DomainID        FlexibleString  `json:"domainId"` // LINE WORKS returns this as a number
	Roles           []string        `json:"roles"`
	ProfileImageURL string          `json:"profileImageUrl"`
}

// WOFFManager manages WOFF OAuth authentication
type WOFFManager struct {
	config      *WOFFConfig
	httpClient  *http.Client
	stateStore  map[string]time.Time // CSRF state storage
	stateMutex  sync.RWMutex
	tokenCache  map[string]*CachedToken // Token cache for validation
	cacheMutex  sync.RWMutex
}

type CachedToken struct {
	UserInfo  *WOFFUserInfo
	ExpiresAt time.Time
}

func NewWOFFManager(config *WOFFConfig) *WOFFManager {
	manager := &WOFFManager{
		config:     config,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		stateStore: make(map[string]time.Time),
		tokenCache: make(map[string]*CachedToken),
	}

	// Start cleanup goroutine for expired states
	go manager.cleanupExpiredStates()

	return manager
}

// GenerateAuthorizationURL creates a WOFF OAuth authorization URL
func (m *WOFFManager) GenerateAuthorizationURL(redirectURI, state string, scopes []string) (string, string, error) {
	// Generate state if not provided
	if state == "" {
		var err error
		state, err = generateRandomState()
		if err != nil {
			return "", "", fmt.Errorf("failed to generate state: %w", err)
		}
	}

	// Store state for verification
	m.stateMutex.Lock()
	m.stateStore[state] = time.Now().Add(10 * time.Minute)
	m.stateMutex.Unlock()

	// Use configured scopes if not provided
	if len(scopes) == 0 {
		scopes = m.config.Scopes
	}

	// Use provided redirect URI or config default
	if redirectURI == "" {
		redirectURI = m.config.RedirectURI
	}

	// Build authorization URL
	params := url.Values{}
	params.Add("client_id", m.config.ClientID)
	params.Add("redirect_uri", redirectURI)
	params.Add("response_type", "code")

	// Only add scope parameter if scopes are provided
	if len(scopes) > 0 {
		params.Add("scope", strings.Join(scopes, " "))
	}

	params.Add("state", state)

	authURL := fmt.Sprintf("%s?%s", WOFFAuthURL, params.Encode())

	return authURL, state, nil
}

// VerifyState verifies the CSRF state parameter
func (m *WOFFManager) VerifyState(state string) bool {
	m.stateMutex.RLock()
	expiresAt, exists := m.stateStore[state]
	m.stateMutex.RUnlock()

	if !exists {
		return false
	}

	if time.Now().After(expiresAt) {
		// State expired
		m.stateMutex.Lock()
		delete(m.stateStore, state)
		m.stateMutex.Unlock()
		return false
	}

	// Remove state after verification (one-time use)
	m.stateMutex.Lock()
	delete(m.stateStore, state)
	m.stateMutex.Unlock()

	return true
}

// ExchangeCode exchanges authorization code for access token
func (m *WOFFManager) ExchangeCode(ctx context.Context, code, redirectURI string) (*WOFFTokenResponse, error) {
	if redirectURI == "" {
		redirectURI = m.config.RedirectURI
	}

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", m.config.ClientID)
	data.Set("client_secret", m.config.ClientSecret)
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, "POST", WOFFTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d, body: %s", ErrWOFFAPIError, resp.StatusCode, string(body))
	}

	var tokenResp WOFFTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}

// GetUserInfo retrieves user information using access token
func (m *WOFFManager) GetUserInfo(ctx context.Context, accessToken string) (*WOFFUserInfo, error) {
	// Check cache first
	m.cacheMutex.RLock()
	cached, exists := m.tokenCache[accessToken]
	m.cacheMutex.RUnlock()

	if exists && time.Now().Before(cached.ExpiresAt) {
		return cached.UserInfo, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", WOFFUserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	authHeader := "Bearer " + accessToken
	req.Header.Set("Authorization", authHeader)
	log.Printf("Calling WOFF UserInfo API - URL: %s, Auth header length: %d", WOFFUserInfoURL, len(authHeader))

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("user info request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d, body: %s", ErrWOFFAPIError, resp.StatusCode, string(body))
	}

	log.Printf("UserInfo API response body: %s", string(body))

	var userInfo WOFFUserInfo
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return nil, fmt.Errorf("failed to parse user info: %w", err)
	}

	// Cache the result
	m.cacheMutex.Lock()
	m.tokenCache[accessToken] = &CachedToken{
		UserInfo:  &userInfo,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	m.cacheMutex.Unlock()

	return &userInfo, nil
}

// RefreshToken refreshes an access token using refresh token
func (m *WOFFManager) RefreshToken(ctx context.Context, refreshToken string) (*WOFFTokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", m.config.ClientID)
	data.Set("client_secret", m.config.ClientSecret)
	data.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, "POST", WOFFTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d, body: %s", ErrWOFFAPIError, resp.StatusCode, string(body))
	}

	var tokenResp WOFFTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}

// VerifyToken verifies if an access token is valid
func (m *WOFFManager) VerifyToken(ctx context.Context, accessToken string) (*WOFFUserInfo, error) {
	return m.GetUserInfo(ctx, accessToken)
}

// cleanupExpiredStates periodically removes expired state entries
func (m *WOFFManager) cleanupExpiredStates() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.stateMutex.Lock()
		now := time.Now()
		for state, expiresAt := range m.stateStore {
			if now.After(expiresAt) {
				delete(m.stateStore, state)
			}
		}
		m.stateMutex.Unlock()

		// Also cleanup expired token cache
		m.cacheMutex.Lock()
		for token, cached := range m.tokenCache {
			if now.After(cached.ExpiresAt) {
				delete(m.tokenCache, token)
			}
		}
		m.cacheMutex.Unlock()
	}
}

// generateRandomState generates a random state string for CSRF protection
func generateRandomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
