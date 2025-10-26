package registration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
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

// MaintainConnection maintains a WebSocket connection to the frontend
// and automatically reconnects on failure
func (r *BackendRegistration) MaintainConnection(backendURL string) {
	log.Printf("🔌 Starting WebSocket connection maintenance")

	for {
		err := r.connectWebSocket(backendURL)
		if err != nil {
			log.Printf("⚠️  WebSocket connection lost: %v", err)
			log.Printf("🔄 Reconnecting in 5 seconds...")
			time.Sleep(5 * time.Second)

			// Re-register before reconnecting
			if regErr := r.RegisterWithRetry(backendURL, 3); regErr != nil {
				log.Printf("❌ Re-registration failed: %v", regErr)
			}
		}
	}
}

// connectWebSocket establishes a WebSocket connection to /wait-for-backend
func (r *BackendRegistration) connectWebSocket(backendURL string) error {
	// Convert https:// to wss:// for WebSocket
	wsURL := r.frontendURL
	if len(wsURL) > 8 && wsURL[:8] == "https://" {
		wsURL = "wss://" + wsURL[8:]
	} else if len(wsURL) > 7 && wsURL[:7] == "http://" {
		wsURL = "ws://" + wsURL[7:]
	}
	wsURL = wsURL + "/wait-for-backend"
	log.Printf("🔌 Connecting to WebSocket: %s", wsURL)

	// Create WebSocket connection
	header := http.Header{}
	header.Set("Authorization", "Bearer "+r.secret)
	header.Set("X-Backend-URL", backendURL)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("websocket dial failed (status %d): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("websocket dial failed: %w", err)
	}
	defer conn.Close()

	log.Printf("✅ WebSocket connected successfully")

	// Set ping/pong handlers for keep-alive
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Send periodic pings
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}()

	// Read messages (mainly for connection monitoring)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("websocket read error: %w", err)
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		log.Printf("📨 WebSocket message received: %s", string(message))
	}
}
