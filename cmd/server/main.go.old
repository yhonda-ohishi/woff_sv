package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	authv1 "github.com/example/jwt-grpc-server/gen/auth/v1"
	"github.com/example/jwt-grpc-server/internal/auth"
	"github.com/example/jwt-grpc-server/internal/interceptor"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const (
	secretKey     = "your-secret-key-change-this-in-production"
	tokenDuration = 15 * time.Minute
	port          = 50051
)

type authServer struct {
	authv1.UnimplementedAuthServiceServer
	jwtManager *auth.JWTManager
	userStore  *auth.UserStore
}

func newAuthServer(jwtManager *auth.JWTManager, userStore *auth.UserStore) *authServer {
	return &authServer{
		jwtManager: jwtManager,
		userStore:  userStore,
	}
}

func (s *authServer) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	log.Printf("Login request for user: %s", req.Username)

	// Authenticate user
	user, err := s.userStore.Authenticate(req.Username, req.Password)
	if err != nil {
		log.Printf("Authentication failed for user %s: %v", req.Username, err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid credentials")
	}

	// Generate access token
	accessToken, err := s.jwtManager.GenerateToken(user.ID, user.Username, user.Email, user.Roles)
	if err != nil {
		log.Printf("Failed to generate token: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to generate token")
	}

	// Generate refresh token (with longer duration)
	refreshToken, err := s.jwtManager.GenerateToken(user.ID, user.Username, user.Email, user.Roles)
	if err != nil {
		log.Printf("Failed to generate refresh token: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to generate refresh token")
	}

	log.Printf("Login successful for user: %s", req.Username)

	return &authv1.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(tokenDuration.Seconds()),
	}, nil
}

func (s *authServer) GetProfile(ctx context.Context, req *authv1.GetProfileRequest) (*authv1.GetProfileResponse, error) {
	// Extract claims from context (added by interceptor)
	claims, ok := interceptor.GetClaimsFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to get user claims")
	}

	log.Printf("GetProfile request for user: %s", claims.Username)

	return &authv1.GetProfileResponse{
		UserId:   claims.UserID,
		Username: claims.Username,
		Email:    claims.Email,
		Roles:    claims.Roles,
	}, nil
}

func (s *authServer) RefreshToken(ctx context.Context, req *authv1.RefreshTokenRequest) (*authv1.RefreshTokenResponse, error) {
	// Validate refresh token
	claims, err := s.jwtManager.ValidateToken(req.RefreshToken)
	if err != nil {
		log.Printf("Invalid refresh token: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid refresh token")
	}

	// Generate new access token
	accessToken, err := s.jwtManager.GenerateToken(claims.UserID, claims.Username, claims.Email, claims.Roles)
	if err != nil {
		log.Printf("Failed to generate new access token: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to generate token")
	}

	log.Printf("Token refreshed for user: %s", claims.Username)

	return &authv1.RefreshTokenResponse{
		AccessToken: accessToken,
		ExpiresIn:   int64(tokenDuration.Seconds()),
	}, nil
}

func main() {
	// Create JWT manager
	jwtManager := auth.NewJWTManager(secretKey, tokenDuration)

	// Create user store with demo user
	userStore := auth.NewUserStore()

	// Public methods that don't require authentication
	publicMethods := []string{
		"/auth.v1.AuthService/Login",
		"/auth.v1.AuthService/RefreshToken",
	}

	// Create auth interceptor
	authInterceptor := interceptor.NewAuthInterceptor(jwtManager, publicMethods)

	// Create gRPC server with interceptors
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(authInterceptor.Unary()),
		grpc.StreamInterceptor(authInterceptor.Stream()),
	)

	// Register auth service
	authService := newAuthServer(jwtManager, userStore)
	authv1.RegisterAuthServiceServer(grpcServer, authService)

	// Register reflection service for grpcurl
	reflection.Register(grpcServer)

	// Start listening
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("gRPC server listening on port %d", port)
	log.Printf("Demo credentials - Username: demo, Password: password123")

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
