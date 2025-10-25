package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/example/jwt-grpc-server/gen/auth/v1"
	"github.com/example/jwt-grpc-server/gen/auth/v1/authv1connect"
	"github.com/example/jwt-grpc-server/internal/auth"
	"github.com/example/jwt-grpc-server/internal/database"
	"github.com/example/jwt-grpc-server/internal/interceptor"
	"github.com/example/jwt-grpc-server/internal/registration"
	"github.com/example/jwt-grpc-server/internal/tunnel"
	"github.com/joho/godotenv"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const (
	defaultPort = 50051
)

// corsMiddleware adds CORS headers to allow browser access
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms")
		w.Header().Set("Access-Control-Expose-Headers", "Connect-Protocol-Version, Connect-Timeout-Ms")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type woffAuthServer struct {
	authv1.UnimplementedAuthServiceServer
	woffManager    *auth.WOFFManager
	woffStore      *database.WOFFStore
	responseCache  sync.Map // codeをキーにしてレスポンスをキャッシュ
	processingCode sync.Map // 処理中のcodeを追跡
}

func newWOFFAuthServer(woffManager *auth.WOFFManager, woffStore *database.WOFFStore) *woffAuthServer {
	return &woffAuthServer{
		woffManager: woffManager,
		woffStore:   woffStore,
	}
}

// Connect-Web handler (wraps gRPC handler)
type connectAuthServer struct {
	grpcServer *woffAuthServer
}

func newConnectAuthServer(grpcServer *woffAuthServer) *connectAuthServer {
	return &connectAuthServer{
		grpcServer: grpcServer,
	}
}

func (s *connectAuthServer) GetAuthorizationURL(ctx context.Context, req *connect.Request[authv1.GetAuthorizationURLRequest]) (*connect.Response[authv1.GetAuthorizationURLResponse], error) {
	resp, err := s.grpcServer.GetAuthorizationURL(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) ExchangeCode(ctx context.Context, req *connect.Request[authv1.ExchangeCodeRequest]) (*connect.Response[authv1.ExchangeCodeResponse], error) {
	resp, err := s.grpcServer.ExchangeCode(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) RefreshToken(ctx context.Context, req *connect.Request[authv1.RefreshTokenRequest]) (*connect.Response[authv1.RefreshTokenResponse], error) {
	resp, err := s.grpcServer.RefreshToken(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) VerifyToken(ctx context.Context, req *connect.Request[authv1.VerifyTokenRequest]) (*connect.Response[authv1.VerifyTokenResponse], error) {
	resp, err := s.grpcServer.VerifyToken(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) GetProfile(ctx context.Context, req *connect.Request[authv1.GetProfileRequest]) (*connect.Response[authv1.GetProfileResponse], error) {
	resp, err := s.grpcServer.GetProfile(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) ListUsers(ctx context.Context, req *connect.Request[authv1.ListUsersRequest]) (*connect.Response[authv1.ListUsersResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/ListUsers")
	resp, err := s.grpcServer.ListUsers(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) UpdateUserRoles(ctx context.Context, req *connect.Request[authv1.UpdateUserRolesRequest]) (*connect.Response[authv1.UpdateUserRolesResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/UpdateUserRoles")
	resp, err := s.grpcServer.UpdateUserRoles(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) DeleteUser(ctx context.Context, req *connect.Request[authv1.DeleteUserRequest]) (*connect.Response[authv1.DeleteUserResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/DeleteUser")
	resp, err := s.grpcServer.DeleteUser(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) RestoreUser(ctx context.Context, req *connect.Request[authv1.RestoreUserRequest]) (*connect.Response[authv1.RestoreUserResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/RestoreUser")
	resp, err := s.grpcServer.RestoreUser(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *woffAuthServer) GetAuthorizationURL(ctx context.Context, req *authv1.GetAuthorizationURLRequest) (*authv1.GetAuthorizationURLResponse, error) {
	log.Printf("GetAuthorizationURL request: redirect_uri=%s, state=%s, scopes=%v", req.RedirectUri, req.State, req.Scopes)

	// WOFF (LINE WORKS) では ID Token発行に "openid" スコープが必須
	// フロントエンドから送信されたスコープを無視して、サーバー設定のデフォルトスコープを使用
	// Use server's configured default scopes (openid) instead of frontend scopes
	authURL, state, err := s.woffManager.GenerateAuthorizationURL(req.RedirectUri, req.State, nil)
	if err != nil {
		log.Printf("Failed to generate authorization URL: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to generate authorization URL: %v", err)
	}

	log.Printf("Generated authorization URL: %s", authURL)
	log.Printf("Generated authorization URL with state: %s", state)

	return &authv1.GetAuthorizationURLResponse{
		AuthorizationUrl: authURL,
		State:            state,
	}, nil
}

func (s *woffAuthServer) ExchangeCode(ctx context.Context, req *authv1.ExchangeCodeRequest) (*authv1.ExchangeCodeResponse, error) {
	log.Printf("ExchangeCode request: code=%s..., redirect_uri=%s", req.Code[:10], req.RedirectUri)

	// キャッシュをチェック - 同じcodeで既に処理済みの場合はキャッシュを返す
	if cachedResp, ok := s.responseCache.Load(req.Code); ok {
		log.Printf("✅ キャッシュされたレスポンスを返します (重複リクエスト対策): code=%s...", req.Code[:10])
		return cachedResp.(*authv1.ExchangeCodeResponse), nil
	}

	// 処理中のcodeをチェック - 既に処理中の場合は少し待ってからキャッシュを返す
	if _, processing := s.processingCode.LoadOrStore(req.Code, true); processing {
		log.Printf("⏳ 同じcodeが処理中です。待機してキャッシュを返します: code=%s...", req.Code[:10])
		// 最大5秒待機
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			if cachedResp, ok := s.responseCache.Load(req.Code); ok {
				log.Printf("✅ 処理完了を検知、キャッシュされたレスポンスを返します: code=%s...", req.Code[:10])
				return cachedResp.(*authv1.ExchangeCodeResponse), nil
			}
		}
		// タイムアウト - state検証エラーを返す代わりに処理を続行
		log.Printf("⚠️ タイムアウト。処理を続行します: code=%s...", req.Code[:10])
	}
	defer s.processingCode.Delete(req.Code)

	// Verify state for CSRF protection
	if req.State != "" {
		if !s.woffManager.VerifyState(req.State) {
			log.Printf("⚠️ Invalid state parameter (既に使用済みの可能性)")
			// state検証失敗でも、キャッシュがあれば返す
			if cachedResp, ok := s.responseCache.Load(req.Code); ok {
				log.Printf("✅ state検証失敗だがキャッシュを返します: code=%s...", req.Code[:10])
				return cachedResp.(*authv1.ExchangeCodeResponse), nil
			}
			return nil, status.Errorf(codes.InvalidArgument, "invalid state parameter")
		}
	}

	// Exchange code for tokens
	tokenResp, err := s.woffManager.ExchangeCode(ctx, req.Code, req.RedirectUri)
	if err != nil {
		log.Printf("Failed to exchange code: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to exchange code: %v", err)
	}

	log.Printf("Token exchange successful - TokenType: %s, ExpiresIn: %d, Scope: %s, AccessToken length: %d, IDToken length: %d",
		tokenResp.TokenType, tokenResp.ExpiresIn, tokenResp.Scope, len(tokenResp.AccessToken), len(tokenResp.IDToken))

	// Get user info with the access token
	userInfo, err := s.woffManager.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		log.Printf("Failed to get user info: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get user info: %v", err)
	}

	// Save user to database (if available)
	if s.woffStore != nil {
		dbUser := &database.WOFFUser{
			UserID:       userInfo.UserID,
			UserName:     userInfo.UserName.FullName(),
			DisplayName:  userInfo.NickName,
			RefreshToken: tokenResp.RefreshToken,
			Roles:        userInfo.Roles,
		}

		if err := s.woffStore.SaveUser(dbUser); err != nil {
			log.Printf("Failed to save user to database: %v", err)
			// Don't fail the request, just log the error
		} else {
			log.Printf("User saved to database: %s", userInfo.UserID)
		}
	}

	log.Printf("Successfully exchanged code for tokens")

	scopes := []string{}
	if tokenResp.Scope != "" {
		scopes = strings.Split(tokenResp.Scope, " ")
	}

	response := &authv1.ExchangeCodeResponse{
		AccessToken:     tokenResp.AccessToken,
		RefreshToken:    tokenResp.RefreshToken,
		ExpiresIn:       tokenResp.ExpiresIn,
		TokenType:       tokenResp.TokenType,
		Scope:           scopes,
		UserId:          userInfo.UserID,
		UserName:        userInfo.UserName.FullName(),
		Email:           userInfo.Email,
		DisplayName:     userInfo.NickName,
		DomainId:        string(userInfo.DomainID),
		Roles:           userInfo.Roles,
		ProfileImageUrl: userInfo.ProfileImageURL,
	}

	// レスポンスをキャッシュ（5分間保持）
	s.responseCache.Store(req.Code, response)
	go func() {
		time.Sleep(5 * time.Minute)
		s.responseCache.Delete(req.Code)
		log.Printf("🗑️ キャッシュを削除: code=%s...", req.Code[:10])
	}()

	log.Printf("ExchangeCode レスポンス返送: ユーザーID=%s, 名前=%s, メール=%s", response.UserId, response.UserName, response.Email)

	return response, nil
}

func (s *woffAuthServer) GetProfile(ctx context.Context, req *authv1.GetProfileRequest) (*authv1.GetProfileResponse, error) {
	// Extract user info from context (added by interceptor)
	userInfo, ok := interceptor.GetWOFFUserInfoFromContext(ctx)
	if !ok {
		log.Printf("⚠️ GetProfile呼び出し - 認証コンテキストなし。ExchangeCodeレスポンスに既にユーザー情報が含まれているため、このAPIは不要です")
		// ExchangeCodeレスポンスに既に全てのユーザー情報が含まれているため、
		// このエンドポイントは後方互換性のためにのみ存在します
		return nil, status.Errorf(codes.Unauthenticated, "authentication required - user info is already included in ExchangeCode response")
	}

	log.Printf("GetProfile request for user: %s", userInfo.UserName.FullName())

	return &authv1.GetProfileResponse{
		UserId:          userInfo.UserID,
		UserName:        userInfo.UserName.FullName(),
		Email:           userInfo.Email,
		DisplayName:     userInfo.NickName, // Use NickName as DisplayName
		DomainId:        string(userInfo.DomainID),
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

func (s *woffAuthServer) ListUsers(ctx context.Context, req *authv1.ListUsersRequest) (*authv1.ListUsersResponse, error) {
	log.Printf("ListUsers request: page=%d, page_size=%d, include_deleted=%v", req.Page, req.PageSize, req.IncludeDeleted)

	// デフォルト値の設定
	page := req.Page
	if page < 1 {
		page = 1
	}

	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 50
	} else if pageSize > 100 {
		pageSize = 100
	}

	// ページネーションの計算
	offset := (page - 1) * pageSize

	// ユーザー一覧を取得
	users, err := s.woffStore.ListUsersWithDeleted(int(pageSize), int(offset), req.IncludeDeleted)
	if err != nil {
		log.Printf("Failed to list users: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to list users: %v", err)
	}

	// 総数を取得
	totalCount, err := s.woffStore.CountUsers(req.IncludeDeleted)
	if err != nil {
		log.Printf("Failed to count users: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to count users: %v", err)
	}

	// プロトコル用のUserメッセージに変換
	pbUsers := make([]*authv1.User, len(users))
	for i, user := range users {
		pbUsers[i] = &authv1.User{
			UserId:      user.UserID,
			UserName:    user.UserName,
			DisplayName: user.DisplayName,
			Roles:       user.Roles,
			CreatedAt:   user.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   user.UpdatedAt.Format(time.RFC3339),
			IsDeleted:   user.DeletedAt != nil,
		}
	}

	log.Printf("Found %d users (total: %d)", len(pbUsers), totalCount)

	return &authv1.ListUsersResponse{
		Users:      pbUsers,
		TotalCount: int32(totalCount),
		Page:       page,
		PageSize:   pageSize,
	}, nil
}

// UpdateUserRoles updates the roles for a specific user (requires admin role)
func (s *woffAuthServer) UpdateUserRoles(ctx context.Context, req *authv1.UpdateUserRolesRequest) (*authv1.UpdateUserRolesResponse, error) {
	log.Printf("UpdateUserRoles request: user_id=%s, roles=%v", req.UserId, req.Roles)

	// Check if database is available
	if s.woffStore == nil {
		return nil, status.Error(codes.Unavailable, "database not available")
	}

	// Validate user ID
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Validate roles
	if len(req.Roles) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one role is required")
	}

	// Update user roles
	if err := s.woffStore.SetRoles(req.UserId, req.Roles); err != nil {
		log.Printf("Failed to update user roles: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to update roles: %v", err)
	}

	log.Printf("✅ Successfully updated roles for user %s: %v", req.UserId, req.Roles)

	return &authv1.UpdateUserRolesResponse{
		Success: true,
		Message: "User roles updated successfully",
		Roles:   req.Roles,
	}, nil
}

// DeleteUser soft deletes a user (requires admin role)
func (s *woffAuthServer) DeleteUser(ctx context.Context, req *authv1.DeleteUserRequest) (*authv1.DeleteUserResponse, error) {
	log.Printf("DeleteUser request: user_id=%s", req.UserId)

	// Check if database is available
	if s.woffStore == nil {
		return nil, status.Error(codes.Unavailable, "database not available")
	}

	// Validate user ID
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Delete user (soft delete)
	if err := s.woffStore.DeleteUser(req.UserId); err != nil {
		log.Printf("Failed to delete user: %v", err)
		// Check if error is about last admin
		if err.Error() == "cannot delete the last admin user" {
			return nil, status.Error(codes.FailedPrecondition, "cannot delete the last admin user")
		}
		return nil, status.Errorf(codes.Internal, "failed to delete user: %v", err)
	}

	log.Printf("✅ Successfully deleted user %s", req.UserId)

	return &authv1.DeleteUserResponse{
		Success: true,
		Message: "User deleted successfully",
	}, nil
}

// RestoreUser restores a soft-deleted user (requires admin role)
func (s *woffAuthServer) RestoreUser(ctx context.Context, req *authv1.RestoreUserRequest) (*authv1.RestoreUserResponse, error) {
	log.Printf("RestoreUser request: user_id=%s", req.UserId)

	// Check if database is available
	if s.woffStore == nil {
		return nil, status.Error(codes.Unavailable, "database not available")
	}

	// Validate user ID
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Restore user
	if err := s.woffStore.RestoreUser(req.UserId); err != nil {
		log.Printf("Failed to restore user: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to restore user: %v", err)
	}

	log.Printf("✅ Successfully restored user %s", req.UserId)

	return &authv1.RestoreUserResponse{
		Success: true,
		Message: "User restored successfully",
	}, nil
}

func main() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	} else {
		log.Println("✅ Loaded configuration from .env file")
	}

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

	// Initialize database (optional)
	var woffStore *database.WOFFStore
	db, err := database.NewDB(dbPath)
	if err != nil {
		log.Printf("⚠️  Database connection failed: %v", err)
		log.Println("⚠️  Running without database (user data will not be persisted)")
		log.Println("💡 To enable database: Install MinGW-w64 (gcc) and rebuild with CGO_ENABLED=1")
	} else {
		defer db.Close()

		// Run migrations
		if err := db.Migrate(); err != nil {
			log.Printf("⚠️  Database migration failed: %v", err)
			log.Println("⚠️  Running without database")
		} else {
			// Create WOFF store
			woffStore = database.NewWOFFStore(db)
			log.Println("✅ Database connected successfully")
		}
	}

	// Create WOFF manager
	woffConfig := &auth.WOFFConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		Scopes:       []string{"openid", "bot", "user.read"}, // openid: ID Token、bot: Access Token、user.read: ユーザー情報取得API
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

	// Create HTTP mux for Connect-Web
	mux := http.NewServeMux()

	// Create Connect interceptor (similar to gRPC interceptor)
	connectInterceptor := func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			log.Printf("Connect request: %s", req.Spec().Procedure)
			return next(ctx, req)
		}
	}

	// Create Connect auth service (wrapper around gRPC service)
	connectService := newConnectAuthServer(authService)

	// Register Connect service with CORS support
	path, handler := authv1connect.NewAuthServiceHandler(
		connectService,
		connect.WithInterceptors(connect.UnaryInterceptorFunc(connectInterceptor)),
	)

	// Add CORS middleware
	mux.Handle(path, corsMiddleware(handler))

	// Create HTTP server that supports both HTTP/1.1 and HTTP/2
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	log.Printf("HTTP/gRPC server with WOFF authentication listening on port %d", port)
	log.Printf("Supporting Connect-Web, gRPC-Web, and gRPC")
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

			// Register backend URL with frontend if configured
			frontendURL := os.Getenv("FRONTEND_URL")
			frontendSecret := os.Getenv("FRONTEND_SECRET")

			if frontendURL != "" && frontendSecret != "" {
				registrar := registration.NewBackendRegistration(frontendURL, frontendSecret)
				go func() {
					if err := registrar.RegisterWithRetry(publicURL, 3); err != nil {
						log.Printf("❌ Failed to register backend with frontend: %v", err)
					}
				}()
			} else {
				if frontendURL == "" {
					log.Println("💡 Tip: Set FRONTEND_URL to auto-register backend with frontend")
				}
				if frontendSecret == "" {
					log.Println("💡 Tip: Set FRONTEND_SECRET to auto-register backend with frontend")
				}
			}
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
		log.Printf("🚀 Server starting on port %d...", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("⚠️  HTTP server shutdown error: %v", err)
	}

	grpcServer.GracefulStop()

	// Stop Cloudflare Tunnel
	if cloudflaredTunnel != nil {
		cloudflaredTunnel.Stop()
	}

	log.Println("✅ Server stopped gracefully")
}
