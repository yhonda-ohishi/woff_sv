package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	authv1 "github.com/example/jwt-grpc-server/gen/auth/v1"
	"github.com/example/jwt-grpc-server/internal/auth"
	"github.com/example/jwt-grpc-server/internal/database"
	"github.com/example/jwt-grpc-server/internal/interceptor"
	"github.com/example/jwt-grpc-server/internal/tunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const (
	defaultPort = 50051
)

type woffAuthServer struct {
	authv1.UnimplementedAuthServiceServer
	woffManager *auth.WOFFManager
	woffStore   *database.WOFFStore
}

func newWOFFAuthServer(woffManager *auth.WOFFManager, woffStore *database.WOFFStore) *woffAuthServer {
	return &woffAuthServer{
		woffManager: woffManager,
		woffStore:   woffStore,
	}
}

func (s *woffAuthServer) GetAuthorizationURL(ctx context.Context, req *authv1.GetAuthorizationURLRequest) (*authv1.GetAuthorizationURLResponse, error) {
	log.Printf("GetAuthorizationURL request: redirect_uri=%s, state=%s", req.RedirectUri, req.State)

	authURL, state, err := s.woffManager.GenerateAuthorizationURL(req.RedirectUri, req.State, req.Scopes)
	if err != nil {
		log.Printf("Failed to generate authorization URL: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to generate authorization URL: %v", err)
	}

	log.Printf("Generated authorization URL with state: %s", state)

	return &authv1.GetAuthorizationURLResponse{
		AuthorizationUrl: authURL,
		State:            state,
	}, nil
}

func (s *woffAuthServer) ExchangeCode(ctx context.Context, req *authv1.ExchangeCodeRequest) (*authv1.ExchangeCodeResponse, error) {
	log.Printf("ExchangeCode request: code=%s..., redirect_uri=%s", req.Code[:10], req.RedirectUri)

	// Verify state for CSRF protection
	if req.State != "" {
		if !s.woffManager.VerifyState(req.State) {
			log.Printf("Invalid state parameter")
			return nil, status.Errorf(codes.InvalidArgument, "invalid state parameter")
		}
	}

	// Exchange code for tokens
	tokenResp, err := s.woffManager.ExchangeCode(ctx, req.Code, req.RedirectUri)
	if err != nil {
		log.Printf("Failed to exchange code: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to exchange code: %v", err)
	}

	// Get user info with the access token
	userInfo, err := s.woffManager.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		log.Printf("Failed to get user info: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get user info: %v", err)
	}

	// Save user to database
	dbUser := &database.WOFFUser{
		UserID:       userInfo.UserID,
		UserName:     userInfo.UserName,
		DisplayName:  userInfo.DisplayName,
		RefreshToken: tokenResp.RefreshToken,
		Roles:        userInfo.Roles,
	}

	if err := s.woffStore.SaveUser(dbUser); err != nil {
		log.Printf("Failed to save user to database: %v", err)
		// Don't fail the request, just log the error
	} else {
		log.Printf("User saved to database: %s", userInfo.UserID)
	}

	log.Printf("Successfully exchanged code for tokens")

	scopes := []string{}
	if tokenResp.Scope != "" {
		scopes = strings.Split(tokenResp.Scope, " ")
	}

	return &authv1.ExchangeCodeResponse{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    tokenResp.ExpiresIn,
		TokenType:    tokenResp.TokenType,
		Scope:        scopes,
	}, nil
}

func (s *woffAuthServer) GetProfile(ctx context.Context, req *authv1.GetProfileRequest) (*authv1.GetProfileResponse, error) {
	// Extract user info from context (added by interceptor)
	userInfo, ok := interceptor.GetWOFFUserInfoFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to get user info from context")
	}

	log.Printf("GetProfile request for user: %s", userInfo.UserName)

	return &authv1.GetProfileResponse{
		UserId:          userInfo.UserID,
		UserName:        userInfo.UserName,
		Email:           userInfo.Email,
		DisplayName:     userInfo.DisplayName,
		DomainId:        userInfo.DomainID,
		Roles:           userInfo.Roles,
		ProfileImageUrl: userInfo.ProfileImageURL,
	}, nil
}

func (s *woffAuthServer) RefreshToken(ctx context.Context, req *authv1.RefreshTokenRequest) (*authv1.RefreshTokenResponse, error) {
	log.Printf("RefreshToken request")

	tokenResp, err := s.woffManager.RefreshToken(ctx, req.RefreshToken)
	if err != nil {
		log.Printf("Failed to refresh token: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to refresh token: %v", err)
	}

	log.Printf("Successfully refreshed token")

	return &authv1.RefreshTokenResponse{
		AccessToken: tokenResp.AccessToken,
		ExpiresIn:   tokenResp.ExpiresIn,
		TokenType:   tokenResp.TokenType,
	}, nil
}

func (s *woffAuthServer) VerifyToken(ctx context.Context, req *authv1.VerifyTokenRequest) (*authv1.VerifyTokenResponse, error) {
	log.Printf("VerifyToken request")

	userInfo, err := s.woffManager.VerifyToken(ctx, req.AccessToken)
	if err != nil {
		log.Printf("Token verification failed: %v", err)
		return &authv1.VerifyTokenResponse{
			Valid: false,
		}, nil
	}

	log.Printf("Token verified for user: %s", userInfo.UserName)

	return &authv1.VerifyTokenResponse{
		Valid:  true,
		UserId: userInfo.UserID,
		// ExpiresAt and Scopes would need to be extracted from token if available
	}, nil
}

func main() {
	// Load configuration from environment variables
	clientID := os.Getenv("WOFF_CLIENT_ID")
	clientSecret := os.Getenv("WOFF_CLIENT_SECRET")
	redirectURI := os.Getenv("WOFF_REDIRECT_URI")
	dbPath := os.Getenv("DATABASE_PATH")

	if clientID == "" || clientSecret == "" {
		log.Fatal("WOFF_CLIENT_ID and WOFF_CLIENT_SECRET must be set")
	}

	if redirectURI == "" {
		redirectURI = "http://localhost:8080/callback"
		log.Printf("WOFF_REDIRECT_URI not set, using default: %s", redirectURI)
	}

	if dbPath == "" {
		dbPath = "woff.db"
		log.Printf("DATABASE_PATH not set, using default: %s", dbPath)
	}

	// Initialize database
	db, err := database.NewDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := db.Migrate(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Create WOFF store
	woffStore := database.NewWOFFStore(db)

	// Create WOFF manager
	woffConfig := &auth.WOFFConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		Scopes:       []string{"user", "user.read"},
	}
	woffManager := auth.NewWOFFManager(woffConfig)

	// Public methods that don't require authentication
	publicMethods := []string{
		"/auth.v1.AuthService/GetAuthorizationURL",
		"/auth.v1.AuthService/ExchangeCode",
		"/auth.v1.AuthService/RefreshToken",
		"/auth.v1.AuthService/VerifyToken",
	}

	// Create WOFF interceptor
	woffInterceptor := interceptor.NewWOFFInterceptor(woffManager, publicMethods)

	// Create gRPC server with interceptors
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(woffInterceptor.Unary()),
		grpc.StreamInterceptor(woffInterceptor.Stream()),
	)

	// Register auth service
	authService := newWOFFAuthServer(woffManager, woffStore)
	authv1.RegisterAuthServiceServer(grpcServer, authService)

	// Register reflection service for grpcurl
	reflection.Register(grpcServer)

	// Get port from environment or use default
	port := defaultPort
	if portEnv := os.Getenv("GRPC_PORT"); portEnv != "" {
		fmt.Sscanf(portEnv, "%d", &port)
	}

	// Check if Cloudflared tunnel should be enabled
	enableTunnel := os.Getenv("ENABLE_CLOUDFLARED") == "true"

	// Start listening
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("gRPC server with WOFF authentication listening on port %d", port)
	log.Printf("WOFF Client ID: %s", clientID)
	log.Printf("Redirect URI: %s", redirectURI)

	// Start Cloudflare Tunnel if enabled
	var cloudflaredTunnel *tunnel.CloudflaredTunnel
	if enableTunnel {
		log.Println("\n🚀 Starting Cloudflare Tunnel...")
		cloudflaredTunnel = tunnel.NewCloudflaredTunnel(port)

		ctx := context.Background()
		publicURL, err := cloudflaredTunnel.Start(ctx)
		if err != nil {
			log.Printf("⚠️  Failed to start Cloudflare Tunnel: %v", err)
			log.Println("Continuing without public URL...")
		} else {
			log.Printf("\n╔════════════════════════════════════════════════════════════╗")
			log.Printf("║  🌐 Public URL: %-42s ║", publicURL)
			log.Printf("║  🔗 gRPC URL:   %-42s ║", cloudflaredTunnel.GetGRPCURL())
			log.Printf("╚════════════════════════════════════════════════════════════╝\n")
		}
	}

	log.Printf("\nTo start authentication flow:")
	log.Printf("1. Call GetAuthorizationURL to get the authorization URL")
	log.Printf("2. Direct user to the authorization URL")
	log.Printf("3. After user authorizes, exchange the code with ExchangeCode")
	log.Printf("4. Use the access token to call GetProfile")

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			serverErr <- err
		}
	}()

	// Wait for shutdown signal or server error
	select {
	case <-sigChan:
		log.Println("\n🛑 Shutdown signal received, stopping server...")
	case err := <-serverErr:
		log.Fatalf("Failed to serve: %v", err)
	}

	// Graceful shutdown
	grpcServer.GracefulStop()

	// Stop Cloudflare Tunnel
	if cloudflaredTunnel != nil {
		cloudflaredTunnel.Stop()
	}

	log.Println("✅ Server stopped gracefully")
}
