package main

import (
	"context"
	"fmt"
	"log"
	"time"

	authv1 "github.com/example/jwt-grpc-server/gen/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	serverAddress = "localhost:50051"
)

type woffClient struct {
	client      authv1.AuthServiceClient
	accessToken string
}

func newWOFFClient(conn *grpc.ClientConn) *woffClient {
	return &woffClient{
		client: authv1.NewAuthServiceClient(conn),
	}
}

func (c *woffClient) getAuthorizationURL(redirectURI, state string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &authv1.GetAuthorizationURLRequest{
		RedirectUri: redirectURI,
		State:       state,
		Scopes:      []string{"user", "user.read"},
	}

	res, err := c.client.GetAuthorizationURL(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("get authorization URL failed: %w", err)
	}

	log.Printf("\nAuthorization URL:")
	log.Printf("  URL: %s", res.AuthorizationUrl)
	log.Printf("  State: %s", res.State)

	return res.AuthorizationUrl, res.State, nil
}

func (c *woffClient) exchangeCode(code, redirectURI, state string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &authv1.ExchangeCodeRequest{
		Code:        code,
		RedirectUri: redirectURI,
		State:       state,
	}

	res, err := c.client.ExchangeCode(ctx, req)
	if err != nil {
		return fmt.Errorf("exchange code failed: %w", err)
	}

	c.accessToken = res.AccessToken

	log.Printf("\nToken Exchange Successful!")
	log.Printf("  Access Token: %s...", res.AccessToken[:40])
	log.Printf("  Refresh Token: %s...", res.RefreshToken[:40])
	log.Printf("  Expires In: %d seconds", res.ExpiresIn)
	log.Printf("  Token Type: %s", res.TokenType)
	log.Printf("  Scopes: %v", res.Scope)

	return nil
}

func (c *woffClient) getProfile() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Add token to metadata
	md := metadata.New(map[string]string{
		"authorization": "Bearer " + c.accessToken,
	})
	ctx = metadata.NewOutgoingContext(ctx, md)

	req := &authv1.GetProfileRequest{}

	res, err := c.client.GetProfile(ctx, req)
	if err != nil {
		return fmt.Errorf("get profile failed: %w", err)
	}

	log.Printf("\nUser Profile:")
	log.Printf("  User ID: %s", res.UserId)
	log.Printf("  Username: %s", res.UserName)
	log.Printf("  Display Name: %s", res.DisplayName)
	log.Printf("  Email: %s", res.Email)
	log.Printf("  Domain ID: %s", res.DomainId)
	log.Printf("  Roles: %v", res.Roles)
	if res.ProfileImageUrl != "" {
		log.Printf("  Profile Image: %s", res.ProfileImageUrl)
	}

	return nil
}

func (c *woffClient) verifyToken() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &authv1.VerifyTokenRequest{
		AccessToken: c.accessToken,
	}

	res, err := c.client.VerifyToken(ctx, req)
	if err != nil {
		return fmt.Errorf("verify token failed: %w", err)
	}

	log.Printf("\nToken Verification:")
	log.Printf("  Valid: %v", res.Valid)
	if res.Valid {
		log.Printf("  User ID: %s", res.UserId)
	}

	return nil
}

func main() {
	// Connect to server
	conn, err := grpc.NewClient(
		serverAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	log.Printf("Connected to gRPC server at %s", serverAddress)

	client := newWOFFClient(conn)

	// Step 1: Get Authorization URL
	log.Println("\n=== Step 1: Get Authorization URL ===")
	redirectURI := "http://localhost:8080/callback"
	authURL, state, err := client.getAuthorizationURL(redirectURI, "")
	if err != nil {
		log.Fatalf("Failed to get authorization URL: %v", err)
	}

	// Step 2: User Authorization (Manual Step)
	log.Println("\n=== Step 2: User Authorization (Manual) ===")
	log.Println("To continue with the WOFF authentication flow:")
	log.Println("1. Open the authorization URL in your browser:")
	log.Printf("   %s\n", authURL)
	log.Println("2. Log in with your LINE WORKS account")
	log.Println("3. After authorization, you will be redirected to the callback URL")
	log.Println("4. Extract the 'code' parameter from the callback URL")
	log.Println("5. Set the code as an environment variable: WOFF_AUTH_CODE=<your_code>")
	log.Println("6. Re-run this client to complete the authentication")

	// For demonstration, we'll try to use a code from environment variable
	// In a real application, you would implement a callback server
	var authCode string
	fmt.Print("\nEnter the authorization code (or press Enter to skip): ")
	fmt.Scanln(&authCode)

	if authCode == "" {
		log.Println("\nNo authorization code provided. Skipping remaining steps.")
		log.Println("This is expected for the first run. Follow the steps above to complete authentication.")
		return
	}

	// Step 3: Exchange Code for Tokens
	log.Println("\n=== Step 3: Exchange Authorization Code ===")
	if err := client.exchangeCode(authCode, redirectURI, state); err != nil {
		log.Fatalf("Failed to exchange code: %v", err)
	}

	// Step 4: Get User Profile
	log.Println("\n=== Step 4: Get User Profile ===")
	if err := client.getProfile(); err != nil {
		log.Fatalf("Failed to get profile: %v", err)
	}

	// Step 5: Verify Token
	log.Println("\n=== Step 5: Verify Token ===")
	if err := client.verifyToken(); err != nil {
		log.Fatalf("Failed to verify token: %v", err)
	}

	log.Println("\n=== WOFF Authentication Flow Completed Successfully! ===")
}
