package main

import (
	"fmt"
	"github.com/example/jwt-grpc-server/internal/flickr"
	"os"
	"encoding/json"
)

type FlickrTokens struct {
	AccessToken  string `json:"access_token"`
	AccessSecret string `json:"access_secret"`
}

func main() {
	// Load tokens
	data, err := os.ReadFile("flickr_tokens.json")
	if err != nil {
		fmt.Printf("Error reading tokens: %v\n", err)
		return
	}

	var tokens FlickrTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		fmt.Printf("Error parsing tokens: %v\n", err)
		return
	}

	// Create client
	apiKey := os.Getenv("FLICKR_API_KEY")
	apiSecret := os.Getenv("FLICKR_API_SECRET")

	client := flickr.NewClientWithToken(apiKey, apiSecret, tokens.AccessToken, tokens.AccessSecret)

	// Try test upload
	photoID, err := client.UploadVideo("test_recordings/test_recording.mp4", "Test Upload", "Testing Flickr upload", false)
	if err != nil {
		fmt.Printf("Upload failed: %v\n", err)
		return
	}

	fmt.Printf("SUCCESS! Photo ID: %s\n", photoID)
}
