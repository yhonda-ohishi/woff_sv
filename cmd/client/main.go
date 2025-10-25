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
	address = "localhost:50051"
)

type authClient struct {
	client      authv1.AuthServiceClient
	accessToken string
}

func newAuthClient(conn *grpc.ClientConn) *authClient {
	return &authClient{
		client: authv1.NewAuthServiceClient(conn),
	}
}

func (c *authClient) login(username, password string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &authv1.LoginRequest{
		Username: username,
		Password: password,
	}

	res, err := c.client.Login(ctx, req)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	c.accessToken = res.AccessToken
	log.Printf("Login successful!")
	log.Printf("Access token: %s...", res.AccessToken[:20])
	log.Printf("Token expires in: %d seconds", res.ExpiresIn)

	return nil
}

func (c *authClient) getProfile() error {
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
	log.Printf("  Username: %s", res.Username)
	log.Printf("  Email: %s", res.Email)
	log.Printf("  Roles: %v", res.Roles)

	return nil
}

func (c *authClient) refreshToken(refreshToken string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &authv1.RefreshTokenRequest{
		RefreshToken: refreshToken,
	}

	res, err := c.client.RefreshToken(ctx, req)
	if err != nil {
		return fmt.Errorf("refresh token failed: %w", err)
	}

	c.accessToken = res.AccessToken
	log.Printf("\nToken refreshed successfully!")
	log.Printf("New access token: %s...", res.AccessToken[:20])
	log.Printf("Token expires in: %d seconds", res.ExpiresIn)

	return nil
}

func main() {
	// Connect to server
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	log.Printf("Connected to gRPC server at %s", address)

	client := newAuthClient(conn)

	// Test 1: Login
	log.Println("\n=== Test 1: Login ===")
	if err := client.login("demo", "password123"); err != nil {
		log.Fatalf("Login test failed: %v", err)
	}

	// Test 2: Get profile with valid token
	log.Println("\n=== Test 2: Get Profile (authenticated) ===")
	if err := client.getProfile(); err != nil {
		log.Fatalf("Get profile test failed: %v", err)
	}

	// Test 3: Try to get profile without token (should fail)
	log.Println("\n=== Test 3: Get Profile (unauthenticated - should fail) ===")
	originalToken := client.accessToken
	client.accessToken = ""
	if err := client.getProfile(); err != nil {
		log.Printf("Expected error: %v", err)
	} else {
		log.Fatal("Should have failed without token")
	}
	client.accessToken = originalToken

	// Test 4: Invalid credentials (should fail)
	log.Println("\n=== Test 4: Login with invalid credentials (should fail) ===")
	if err := client.login("demo", "wrongpassword"); err != nil {
		log.Printf("Expected error: %v", err)
	} else {
		log.Fatal("Should have failed with wrong password")
	}

	log.Println("\n=== All tests completed successfully! ===")
}
