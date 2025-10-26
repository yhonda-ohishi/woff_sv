package flickr

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	APIKey       string
	APISecret    string
	AccessToken  string
	AccessSecret string
	HTTPClient   *http.Client
}

func NewClient(apiKey, apiSecret string) *Client {
	return &Client{
		APIKey:     apiKey,
		APISecret:  apiSecret,
		HTTPClient: &http.Client{},
	}
}

func NewClientWithToken(apiKey, apiSecret, accessToken, accessSecret string) *Client {
	return &Client{
		APIKey:       apiKey,
		APISecret:    apiSecret,
		AccessToken:  accessToken,
		AccessSecret: accessSecret,
		HTTPClient:   &http.Client{},
	}
}

// generateNonce generates a random nonce for OAuth
func generateNonce() string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 32)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// UploadVideo uploads a video file to Flickr using OAuth
func (c *Client) UploadVideo(filePath, title, description string, isPublic bool) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add form fields
	writer.WriteField("title", title)
	writer.WriteField("description", description)
	if isPublic {
		writer.WriteField("is_public", "1")
	} else {
		writer.WriteField("is_public", "0")
	}

	// Add photo file
	part, err := writer.CreateFormFile("photo", filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("failed to copy file: %w", err)
	}

	contentType := writer.FormDataContentType()
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close writer: %w", err)
	}

	// Build OAuth parameters
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := generateNonce()

	// Collect all parameters for signature (OAuth + form params)
	allParams := make(map[string]string)
	allParams["oauth_consumer_key"] = c.APIKey
	allParams["oauth_nonce"] = nonce
	allParams["oauth_signature_method"] = "HMAC-SHA1"
	allParams["oauth_timestamp"] = timestamp
	allParams["oauth_token"] = c.AccessToken
	allParams["oauth_version"] = "1.0"
	allParams["title"] = title
	allParams["description"] = description
	if isPublic {
		allParams["is_public"] = "1"
	} else {
		allParams["is_public"] = "0"
	}

	// Sort all parameters
	keys := make([]string, 0, len(allParams))
	for k := range allParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build parameter string
	// IMPORTANT: Use RFC 3986 encoding (percent encoding) instead of QueryEscape
	// QueryEscape uses + for spaces, but OAuth requires %20
	var paramParts []string
	for _, k := range keys {
		encodedKey := strings.ReplaceAll(url.QueryEscape(k), "+", "%20")
		encodedVal := strings.ReplaceAll(url.QueryEscape(allParams[k]), "+", "%20")
		paramParts = append(paramParts, encodedKey+"="+encodedVal)
	}
	paramString := strings.Join(paramParts, "&")

	// Build signature base string
	baseURL := "https://up.flickr.com/services/upload/"
	signatureBase := "POST&" + url.QueryEscape(baseURL) + "&" + url.QueryEscape(paramString)

	// Build signing key
	signingKey := url.QueryEscape(c.APISecret) + "&" + url.QueryEscape(c.AccessSecret)

	// Generate HMAC-SHA1 signature
	mac := hmac.New(sha1.New, []byte(signingKey))
	mac.Write([]byte(signatureBase))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// Log signature details for debugging
	log.Printf("🔍 OAuth Debug:")
	log.Printf("  Signature Base String: %s", signatureBase)
	log.Printf("  Signing Key: %s", signingKey)
	log.Printf("  Generated Signature: %s", signature)

	// Build Authorization header (OAuth params only, not form params)
	authParts := []string{
		`oauth_consumer_key="` + url.QueryEscape(c.APIKey) + `"`,
		`oauth_nonce="` + url.QueryEscape(nonce) + `"`,
		`oauth_signature="` + url.QueryEscape(signature) + `"`,
		`oauth_signature_method="HMAC-SHA1"`,
		`oauth_timestamp="` + url.QueryEscape(timestamp) + `"`,
		`oauth_token="` + url.QueryEscape(c.AccessToken) + `"`,
		`oauth_version="1.0"`,
	}
	authHeader := "OAuth " + strings.Join(authParts, ", ")

	// Create request
	req, err := http.NewRequest("POST", baseURL, body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", authHeader)

	// Send request
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Extract photo ID from XML response
	photoID := extractPhotoID(string(respBody))
	if photoID == "" {
		return "", fmt.Errorf("failed to extract photo ID from response: %s", string(respBody))
	}

	return photoID, nil
}

// extractPhotoID extracts photo ID from Flickr XML response
func extractPhotoID(xmlResp string) string {
	// Simple XML parsing for <photoid>...</photoid>
	start := 0
	for i := 0; i < len(xmlResp)-9; i++ {
		if xmlResp[i:i+9] == "<photoid>" {
			start = i + 9
			break
		}
	}
	if start == 0 {
		return ""
	}

	end := 0
	for i := start; i < len(xmlResp)-10; i++ {
		if xmlResp[i:i+10] == "</photoid>" {
			end = i
			break
		}
	}
	if end == 0 {
		return ""
	}

	return xmlResp[start:end]
}
