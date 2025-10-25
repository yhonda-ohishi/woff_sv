package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CloudflaredTunnel manages a Cloudflare Tunnel
type CloudflaredTunnel struct {
	port      int
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	publicURL string
	mu        sync.RWMutex
}

// NewCloudflaredTunnel creates a new Cloudflare Tunnel manager
func NewCloudflaredTunnel(port int) *CloudflaredTunnel {
	return &CloudflaredTunnel{
		port: port,
	}
}

// Start starts the Cloudflare Tunnel and returns the public URL
func (t *CloudflaredTunnel) Start(ctx context.Context) (string, error) {
	// Create a cancellable context
	tunnelCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel

	// Start cloudflared tunnel
	t.cmd = exec.CommandContext(tunnelCtx, "cloudflared", "tunnel", "--url", fmt.Sprintf("http://localhost:%d", t.port))

	// Get stdout and stderr pipes
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	// Start the command
	if err := t.cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start cloudflared: %w", err)
	}

	log.Println("Starting Cloudflare Tunnel...")

	// Channel to receive the public URL
	urlChan := make(chan string, 1)
	errChan := make(chan error, 1)

	// Regex to extract the public URL
	urlRegex := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

	// Read stdout in a goroutine
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[Cloudflared] %s", line)

			// Look for the public URL
			if matches := urlRegex.FindString(line); matches != "" {
				select {
				case urlChan <- matches:
				default:
				}
			}
		}
	}()

	// Read stderr in a goroutine
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[Cloudflared] %s", line)

			// Look for the public URL in stderr too
			if matches := urlRegex.FindString(line); matches != "" {
				select {
				case urlChan <- matches:
				default:
				}
			}
		}
	}()

	// Wait for the URL with timeout
	select {
	case publicURL := <-urlChan:
		t.mu.Lock()
		t.publicURL = publicURL
		t.mu.Unlock()
		log.Printf("✅ Cloudflare Tunnel started successfully!")
		log.Printf("🌐 Public URL: %s", publicURL)
		return publicURL, nil

	case err := <-errChan:
		t.Stop()
		return "", err

	case <-time.After(30 * time.Second):
		t.Stop()
		return "", fmt.Errorf("timeout waiting for Cloudflare Tunnel URL")
	}
}

// GetPublicURL returns the current public URL
func (t *CloudflaredTunnel) GetPublicURL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.publicURL
}

// Stop stops the Cloudflare Tunnel
func (t *CloudflaredTunnel) Stop() error {
	if t.cancel != nil {
		t.cancel()
	}

	if t.cmd != nil && t.cmd.Process != nil {
		log.Println("Stopping Cloudflare Tunnel...")
		if err := t.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to stop cloudflared: %w", err)
		}
		t.cmd.Wait()
	}

	t.mu.Lock()
	t.publicURL = ""
	t.mu.Unlock()

	log.Println("Cloudflare Tunnel stopped")
	return nil
}

// IsRunning returns true if the tunnel is running
func (t *CloudflaredTunnel) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.publicURL != ""
}

// GetGRPCURL returns the gRPC URL (without http://)
func (t *CloudflaredTunnel) GetGRPCURL() string {
	url := t.GetPublicURL()
	if url == "" {
		return ""
	}
	// Remove https:// prefix for gRPC usage
	return strings.TrimPrefix(url, "https://")
}
