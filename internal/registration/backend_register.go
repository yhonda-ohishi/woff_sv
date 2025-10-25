package registration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// BackendRegistration handles registering the backend URL with the frontend
type BackendRegistration struct {
	frontendURL string
	secret      string
	client      *http.Client
}

type registerRequest struct {
	URL string `json:"url"`
}

type registerResponse struct {
	Success bool   `json:"success"`
	URL     string `json:"url"`
	Error   string `json:"error,omitempty"`
}

// NewBackendRegistration creates a new backend registration client
func NewBackendRegistration(frontendURL, secret string) *BackendRegistration {
	return &BackendRegistration{
		frontendURL: frontendURL,
		secret:      secret,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Register registers the backend URL with the frontend
func (r *BackendRegistration) Register(backendURL string) error {
	log.Printf("🔄 Registering backend URL with frontend...")
	log.Printf("   Frontend: %s", r.frontendURL)
	log.Printf("   Backend URL: %s", backendURL)

	// Prepare request payload
	payload := registerRequest{
		URL: backendURL,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	endpoint := r.frontendURL + "/register-backend"
	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.secret)

	// Send request
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check status code
	if resp.StatusCode == 401 {
		return fmt.Errorf("unauthorized: invalid secret")
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("registration failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var result registerResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("registration failed: %s", result.Error)
	}

	log.Printf("✅ Backend URL registered successfully!")
	log.Printf("   Registered URL: %s", result.URL)

	return nil
}

// RegisterWithRetry attempts to register with retry logic
func (r *BackendRegistration) RegisterWithRetry(backendURL string, maxRetries int) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			waitTime := time.Duration(i*2) * time.Second
			log.Printf("⏳ Retrying registration in %v... (attempt %d/%d)", waitTime, i+1, maxRetries)
			time.Sleep(waitTime)
		}

		err := r.Register(backendURL)
		if err == nil {
			return nil
		}

		lastErr = err
		log.Printf("⚠️  Registration attempt %d/%d failed: %v", i+1, maxRetries, err)
	}

	return fmt.Errorf("registration failed after %d attempts: %w", maxRetries, lastErr)
}
