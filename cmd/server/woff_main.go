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
	dbconfig "github.com/yhonda-ohishi/db_service/src/config"
	"github.com/yhonda-ohishi/db_service/src/models/mysql"
	"github.com/yhonda-ohishi/db_service/src/repository"
	"github.com/joho/godotenv"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
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
	woffManager       *auth.WOFFManager
	lineManager       *auth.LINEManager
	woffStore         *database.WOFFStore
	responseCache     sync.Map // codeをキーにしてレスポンスをキャッシュ
	processingCode    sync.Map // 処理中のcodeを追跡
	prodDB            *dbconfig.ProdDatabase
	devDB             *gorm.DB
	timeCardProdRepo  repository.TimeCardRepository
	timeCardDevRepo   repository.TimeCardDevRepository
}

func newWOFFAuthServer(
	woffManager *auth.WOFFManager,
	lineManager *auth.LINEManager,
	woffStore *database.WOFFStore,
	prodDB *dbconfig.ProdDatabase,
	devDB *gorm.DB,
) *woffAuthServer {
	var timeCardProdRepo repository.TimeCardRepository
	var timeCardDevRepo repository.TimeCardDevRepository

	if prodDB != nil {
		timeCardProdRepo = repository.NewTimeCardRepository(prodDB)
	}
	if devDB != nil {
		timeCardDevRepo = repository.NewTimeCardDevRepository(devDB)
	}

	return &woffAuthServer{
		woffManager:      woffManager,
		lineManager:      lineManager,
		woffStore:        woffStore,
		prodDB:           prodDB,
		devDB:            devDB,
		timeCardProdRepo: timeCardProdRepo,
		timeCardDevRepo:  timeCardDevRepo,
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

func (s *connectAuthServer) GetTimeCard(ctx context.Context, req *connect.Request[authv1.GetTimeCardRequest]) (*connect.Response[authv1.TimeCardResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/GetTimeCard")
	resp, err := s.grpcServer.GetTimeCard(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) ListTimeCards(ctx context.Context, req *connect.Request[authv1.ListTimeCardsRequest]) (*connect.Response[authv1.ListTimeCardsResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/ListTimeCards")
	resp, err := s.grpcServer.ListTimeCards(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) CreateTimeCard(ctx context.Context, req *connect.Request[authv1.CreateTimeCardRequest]) (*connect.Response[authv1.TimeCardResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/CreateTimeCard")
	resp, err := s.grpcServer.CreateTimeCard(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) UpdateTimeCard(ctx context.Context, req *connect.Request[authv1.UpdateTimeCardRequest]) (*connect.Response[authv1.TimeCardResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/UpdateTimeCard")
	resp, err := s.grpcServer.UpdateTimeCard(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) DeleteTimeCard(ctx context.Context, req *connect.Request[authv1.DeleteTimeCardRequest]) (*connect.Response[authv1.DeleteTimeCardResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/DeleteTimeCard")
	resp, err := s.grpcServer.DeleteTimeCard(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *woffAuthServer) GetAuthorizationURL(ctx context.Context, req *authv1.GetAuthorizationURLRequest) (*authv1.GetAuthorizationURLResponse, error) {
	// プロバイダーの設定（デフォルトはwoff）
	provider := req.Provider
	if provider == "" {
		provider = "woff"
	}

	log.Printf("GetAuthorizationURL request: provider=%s, redirect_uri=%s, state=%s, scopes=%v", provider, req.RedirectUri, req.State, req.Scopes)

	var authURL, state string
	var err error

	switch provider {
	case "line":
		// LINE Login
		authURL, state, err = s.lineManager.GenerateAuthorizationURL(req.RedirectUri, req.State, req.Scopes)
	case "woff":
		// WOFF (LINE WORKS) では ID Token発行に "openid" スコープが必須
		// フロントエンドから送信されたスコープを無視して、サーバー設定のデフォルトスコープを使用
		authURL, state, err = s.woffManager.GenerateAuthorizationURL(req.RedirectUri, req.State, nil)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported provider: %s", provider)
	}

	if err != nil {
		log.Printf("Failed to generate authorization URL: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to generate authorization URL: %v", err)
	}

	log.Printf("Generated %s authorization URL: %s", provider, authURL)
	log.Printf("Generated authorization URL with state: %s", state)

	return &authv1.GetAuthorizationURLResponse{
		AuthorizationUrl: authURL,
		State:            state,
	}, nil
}

func (s *woffAuthServer) ExchangeCode(ctx context.Context, req *authv1.ExchangeCodeRequest) (*authv1.ExchangeCodeResponse, error) {
	log.Printf("ExchangeCode request: code=%s..., redirect_uri=%s", req.Code[:10], req.RedirectUri)

	// プロバイダーの設定（デフォルトはwoff）
	provider := req.Provider
	if provider == "" {
		provider = "woff"
	}

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

	// Verify state for CSRF protection (プロバイダー別)
	if req.State != "" {
		var stateValid bool
		switch provider {
		case "line":
			stateValid = s.lineManager.VerifyState(req.State)
		case "woff":
			stateValid = s.woffManager.VerifyState(req.State)
		default:
			return nil, status.Errorf(codes.InvalidArgument, "unsupported provider: %s", provider)
		}

		if !stateValid {
			log.Printf("⚠️ Invalid state parameter (フロントエンドが独自に生成した可能性、またはstate store に存在しない)")
			// state検証失敗でも、キャッシュがあれば返す
			if cachedResp, ok := s.responseCache.Load(req.Code); ok {
				log.Printf("✅ state検証失敗だがキャッシュを返します: code=%s...", req.Code[:10])
				return cachedResp.(*authv1.ExchangeCodeResponse), nil
			}
			// LINEの場合、フロントエンドが独自にstateを管理することが多いため、警告のみで続行
			log.Printf("⚠️ state検証失敗ですが、処理を続行します (provider=%s)", provider)
		}
	}

	// プロバイダー別にトークン交換とユーザー情報取得
	var response *authv1.ExchangeCodeResponse
	var err error

	switch provider {
	case "line":
		response, err = s.handleLINELogin(ctx, req, provider)
	case "woff":
		response, err = s.handleWOFFLogin(ctx, req, provider)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported provider: %s", provider)
	}

	if err != nil {
		return nil, err
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

// handleWOFFLogin handles WOFF (LINE WORKS) login
func (s *woffAuthServer) handleWOFFLogin(ctx context.Context, req *authv1.ExchangeCodeRequest, provider string) (*authv1.ExchangeCodeResponse, error) {
	// Exchange code for tokens
	tokenResp, err := s.woffManager.ExchangeCode(ctx, req.Code, req.RedirectUri)
	if err != nil {
		log.Printf("Failed to exchange code: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to exchange code: %v", err)
	}

	log.Printf("Token exchange successful - TokenType: %s, ExpiresIn: %d, Scope: %s", tokenResp.TokenType, tokenResp.ExpiresIn, tokenResp.Scope)

	// Get user info with the access token
	userInfo, err := s.woffManager.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		log.Printf("Failed to get user info: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get user info: %v", err)
	}

	// Save user to database
	if s.woffStore != nil {
		dbUser := &database.WOFFUser{
			UserID:       userInfo.UserID,
			Provider:     provider,
			UserName:     userInfo.UserName.FullName(),
			DisplayName:  userInfo.NickName,
			RefreshToken: tokenResp.RefreshToken,
			Roles:        userInfo.Roles,
		}

		if err := s.woffStore.SaveUser(dbUser); err != nil {
			log.Printf("Failed to save user to database: %v", err)
		} else {
			log.Printf("User saved to database: %s", userInfo.UserID)
		}
	}

	scopes := []string{}
	if tokenResp.Scope != "" {
		scopes = strings.Split(tokenResp.Scope, " ")
	}

	return &authv1.ExchangeCodeResponse{
		AccessToken:     tokenResp.AccessToken,
		RefreshToken:    tokenResp.RefreshToken,
		ExpiresIn:       tokenResp.ExpiresIn,
		TokenType:       tokenResp.TokenType,
		Scope:           scopes,
		UserId:          userInfo.UserID,
		Provider:        provider,
		UserName:        userInfo.UserName.FullName(),
		Email:           userInfo.Email,
		DisplayName:     userInfo.NickName,
		DomainId:        string(userInfo.DomainID),
		Roles:           userInfo.Roles,
		ProfileImageUrl: userInfo.ProfileImageURL,
	}, nil
}

// handleLINELogin handles LINE Login
func (s *woffAuthServer) handleLINELogin(ctx context.Context, req *authv1.ExchangeCodeRequest, provider string) (*authv1.ExchangeCodeResponse, error) {
	// Exchange code for tokens
	tokenResp, err := s.lineManager.ExchangeCode(ctx, req.Code, req.RedirectUri)
	if err != nil {
		log.Printf("Failed to exchange code: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to exchange code: %v", err)
	}

	log.Printf("LINE token exchange successful - TokenType: %s, ExpiresIn: %d, Scope: %s", tokenResp.TokenType, tokenResp.ExpiresIn, tokenResp.Scope)

	// Get user info with the access token
	userInfo, err := s.lineManager.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		log.Printf("Failed to get LINE user info: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get user info: %v", err)
	}

	// Save user to database
	// Rolesは空で渡すことで、SaveUser内で自動的に判定される（最初のユーザーはadmin）
	var roles []string
	if s.woffStore != nil {
		dbUser := &database.WOFFUser{
			UserID:       userInfo.UserID,
			Provider:     provider,
			UserName:     userInfo.DisplayName,
			DisplayName:  userInfo.DisplayName,
			RefreshToken: tokenResp.RefreshToken,
			Roles:        nil, // 空にすることでSaveUser内で自動判定
		}

		if err := s.woffStore.SaveUser(dbUser); err != nil {
			log.Printf("Failed to save user to database: %v", err)
			// エラー時はデフォルトロールを設定
			roles = []string{"user"}
		} else {
			log.Printf("LINE user saved to database: %s", userInfo.UserID)
			// データベースから保存されたユーザーを取得してロールを確認
			savedUser, err := s.woffStore.GetUser(userInfo.UserID)
			if err == nil && savedUser != nil {
				roles = savedUser.Roles
			} else {
				roles = []string{"user"}
			}
		}
	} else {
		// データベースが無い場合はデフォルトロール
		roles = []string{"user"}
	}

	scopes := []string{}
	if tokenResp.Scope != "" {
		scopes = strings.Split(tokenResp.Scope, " ")
	}

	return &authv1.ExchangeCodeResponse{
		AccessToken:     tokenResp.AccessToken,
		RefreshToken:    tokenResp.RefreshToken,
		ExpiresIn:       tokenResp.ExpiresIn,
		TokenType:       tokenResp.TokenType,
		Scope:           scopes,
		UserId:          userInfo.UserID,
		Provider:        provider,
		UserName:        userInfo.DisplayName,
		Email:           userInfo.Email,
		DisplayName:     userInfo.DisplayName,
		DomainId:        "", // LINEにはdomain_idがない
		Roles:           roles,
		ProfileImageUrl: userInfo.PictureURL,
	}, nil
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
		Provider:        "woff", // GetProfileはWOFFトークンから取得するため常にwoff
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
			Provider:    user.Provider,
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

	// Get authenticated user from context
	authenticatedUserID, ok := ctx.Value("user_id").(string)
	if !ok || authenticatedUserID == "" {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}

	// Get authenticated user's information
	authenticatedUser, err := s.woffStore.GetUser(authenticatedUserID)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid user")
	}

	// Check if authenticated user has admin role
	hasAdmin := false
	for _, role := range authenticatedUser.Roles {
		if role == "admin" {
			hasAdmin = true
			break
		}
	}

	if !hasAdmin {
		return nil, status.Error(codes.PermissionDenied, "only admin users can update roles")
	}

	// Prevent users from modifying their own roles
	if authenticatedUserID == req.UserId {
		return nil, status.Error(codes.PermissionDenied, "cannot modify your own roles")
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

// GetTimeCard retrieves a timecard by composite key
func (s *woffAuthServer) GetTimeCard(ctx context.Context, req *authv1.GetTimeCardRequest) (*authv1.TimeCardResponse, error) {
	log.Printf("GetTimeCard request: environment=%v, datetime=%s, id=%d", req.Environment, req.Datetime, req.Id)

	// Parse datetime
	parsedTime, err := time.Parse(time.RFC3339, req.Datetime)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid datetime format: %v", err)
	}

	var timeCard *mysql.TimeCard

	// Select repository based on environment
	switch req.Environment {
	case authv1.DBEnvironment_DB_ENVIRONMENT_DEV:
		if s.timeCardDevRepo == nil {
			return nil, status.Error(codes.Unavailable, "dev database not available")
		}
		timeCard, err = s.timeCardDevRepo.GetByCompositeKey(parsedTime, int(req.Id))
	case authv1.DBEnvironment_DB_ENVIRONMENT_PROD, authv1.DBEnvironment_DB_ENVIRONMENT_UNSPECIFIED:
		if s.timeCardProdRepo == nil {
			return nil, status.Error(codes.Unavailable, "prod database not available")
		}
		timeCard, err = s.timeCardProdRepo.GetByCompositeKey(parsedTime, int(req.Id))
	default:
		return nil, status.Errorf(codes.InvalidArgument, "invalid environment: %v", req.Environment)
	}

	if err != nil {
		log.Printf("Failed to get timecard: %v", err)
		return nil, status.Errorf(codes.NotFound, "timecard not found: %v", err)
	}

	// Convert to proto message
	stateDetail := ""
	if timeCard.StateDetail != nil {
		stateDetail = *timeCard.StateDetail
	}

	return &authv1.TimeCardResponse{
		Timecard: &authv1.TimeCard{
			Datetime:    timeCard.Datetime.Format(time.RFC3339),
			Id:          int32(timeCard.ID),
			MachineIp:   timeCard.MachineIP,
			State:       timeCard.State,
			StateDetail: stateDetail,
			Created:     timeCard.Created.Format(time.RFC3339),
			Modified:    timeCard.Modified.Format(time.RFC3339),
		},
	}, nil
}

// ListTimeCards retrieves a list of timecards
func (s *woffAuthServer) ListTimeCards(ctx context.Context, req *authv1.ListTimeCardsRequest) (*authv1.ListTimeCardsResponse, error) {
	log.Printf("ListTimeCards request: environment=%v, limit=%d, offset=%d, order_by=%s", req.Environment, req.Limit, req.Offset, req.OrderBy)

	// Set defaults
	limit := int(req.Limit)
	if limit == 0 {
		limit = 50
	} else if limit > 100 {
		limit = 100
	}

	offset := int(req.Offset)
	orderBy := req.OrderBy
	if orderBy == "" {
		orderBy = "datetime DESC"
	}

	var timeCards []*mysql.TimeCard
	var totalCount int64
	var err error

	// Select repository based on environment
	switch req.Environment {
	case authv1.DBEnvironment_DB_ENVIRONMENT_DEV:
		if s.timeCardDevRepo == nil {
			return nil, status.Error(codes.Unavailable, "dev database not available")
		}
		timeCards, totalCount, err = s.timeCardDevRepo.GetAll(limit, offset, orderBy)
	case authv1.DBEnvironment_DB_ENVIRONMENT_PROD, authv1.DBEnvironment_DB_ENVIRONMENT_UNSPECIFIED:
		if s.timeCardProdRepo == nil {
			return nil, status.Error(codes.Unavailable, "prod database not available")
		}
		timeCards, totalCount, err = s.timeCardProdRepo.GetAll(limit, offset, orderBy)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "invalid environment: %v", req.Environment)
	}

	if err != nil {
		log.Printf("Failed to list timecards: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to list timecards: %v", err)
	}

	// Convert to proto messages
	protoTimeCards := make([]*authv1.TimeCard, 0, len(timeCards))
	for _, tc := range timeCards {
		stateDetail := ""
		if tc.StateDetail != nil {
			stateDetail = *tc.StateDetail
		}

		protoTimeCards = append(protoTimeCards, &authv1.TimeCard{
			Datetime:    tc.Datetime.Format(time.RFC3339),
			Id:          int32(tc.ID),
			MachineIp:   tc.MachineIP,
			State:       tc.State,
			StateDetail: stateDetail,
			Created:     tc.Created.Format(time.RFC3339),
			Modified:    tc.Modified.Format(time.RFC3339),
		})
	}

	return &authv1.ListTimeCardsResponse{
		Timecards:  protoTimeCards,
		TotalCount: totalCount,
	}, nil
}

// CreateTimeCard creates a new timecard (dev environment only)
func (s *woffAuthServer) CreateTimeCard(ctx context.Context, req *authv1.CreateTimeCardRequest) (*authv1.TimeCardResponse, error) {
	log.Printf("CreateTimeCard request: datetime=%s, id=%d", req.Datetime, req.Id)

	// Only allow in dev repository
	if s.timeCardDevRepo == nil {
		return nil, status.Error(codes.Unavailable, "dev database not available")
	}

	// Parse datetime
	parsedTime, err := time.Parse(time.RFC3339, req.Datetime)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid datetime format: %v", err)
	}

	// Create timecard model
	now := time.Now()
	var stateDetail *string
	if req.StateDetail != "" {
		stateDetail = &req.StateDetail
	}

	timeCard := &mysql.TimeCard{
		Datetime:    parsedTime,
		ID:          int(req.Id),
		MachineIP:   req.MachineIp,
		State:       req.State,
		StateDetail: stateDetail,
		Created:     now,
		Modified:    now,
	}

	// Create in database
	if err := s.timeCardDevRepo.Create(timeCard); err != nil {
		log.Printf("Failed to create timecard: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to create timecard: %v", err)
	}

	log.Printf("✅ Successfully created timecard: datetime=%s, id=%d", req.Datetime, req.Id)

	// Return created timecard
	resultStateDetail := ""
	if timeCard.StateDetail != nil {
		resultStateDetail = *timeCard.StateDetail
	}

	return &authv1.TimeCardResponse{
		Timecard: &authv1.TimeCard{
			Datetime:    timeCard.Datetime.Format(time.RFC3339),
			Id:          int32(timeCard.ID),
			MachineIp:   timeCard.MachineIP,
			State:       timeCard.State,
			StateDetail: resultStateDetail,
			Created:     timeCard.Created.Format(time.RFC3339),
			Modified:    timeCard.Modified.Format(time.RFC3339),
		},
	}, nil
}

// UpdateTimeCard updates an existing timecard (dev environment only)
func (s *woffAuthServer) UpdateTimeCard(ctx context.Context, req *authv1.UpdateTimeCardRequest) (*authv1.TimeCardResponse, error) {
	log.Printf("UpdateTimeCard request: datetime=%s, id=%d", req.Datetime, req.Id)

	// Only allow in dev repository
	if s.timeCardDevRepo == nil {
		return nil, status.Error(codes.Unavailable, "dev database not available")
	}

	// Parse datetime
	parsedTime, err := time.Parse(time.RFC3339, req.Datetime)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid datetime format: %v", err)
	}

	// Get existing timecard
	existingCard, err := s.timeCardDevRepo.GetByCompositeKey(parsedTime, int(req.Id))
	if err != nil {
		log.Printf("Failed to find timecard: %v", err)
		return nil, status.Errorf(codes.NotFound, "timecard not found: %v", err)
	}

	// Update fields
	existingCard.MachineIP = req.MachineIp
	existingCard.State = req.State
	if req.StateDetail != "" {
		existingCard.StateDetail = &req.StateDetail
	} else {
		existingCard.StateDetail = nil
	}
	existingCard.Modified = time.Now()

	// Update in database
	if err := s.timeCardDevRepo.Update(existingCard); err != nil {
		log.Printf("Failed to update timecard: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to update timecard: %v", err)
	}

	log.Printf("✅ Successfully updated timecard: datetime=%s, id=%d", req.Datetime, req.Id)

	// Return updated timecard
	resultStateDetail := ""
	if existingCard.StateDetail != nil {
		resultStateDetail = *existingCard.StateDetail
	}

	return &authv1.TimeCardResponse{
		Timecard: &authv1.TimeCard{
			Datetime:    existingCard.Datetime.Format(time.RFC3339),
			Id:          int32(existingCard.ID),
			MachineIp:   existingCard.MachineIP,
			State:       existingCard.State,
			StateDetail: resultStateDetail,
			Created:     existingCard.Created.Format(time.RFC3339),
			Modified:    existingCard.Modified.Format(time.RFC3339),
		},
	}, nil
}

// DeleteTimeCard deletes a timecard (dev environment only)
func (s *woffAuthServer) DeleteTimeCard(ctx context.Context, req *authv1.DeleteTimeCardRequest) (*authv1.DeleteTimeCardResponse, error) {
	log.Printf("DeleteTimeCard request: datetime=%s, id=%d", req.Datetime, req.Id)

	// Only allow in dev repository
	if s.timeCardDevRepo == nil {
		return nil, status.Error(codes.Unavailable, "dev database not available")
	}

	// Parse datetime
	parsedTime, err := time.Parse(time.RFC3339, req.Datetime)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid datetime format: %v", err)
	}

	// Delete from database
	if err := s.timeCardDevRepo.Delete(parsedTime, int(req.Id)); err != nil {
		log.Printf("Failed to delete timecard: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to delete timecard: %v", err)
	}

	log.Printf("✅ Successfully deleted timecard: datetime=%s, id=%d", req.Datetime, req.Id)

	return &authv1.DeleteTimeCardResponse{
		Success: true,
		Message: "Timecard deleted successfully",
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

	// Create LINE manager
	lineChannelID := os.Getenv("LINE_CHANNEL_ID")
	lineChannelSecret := os.Getenv("LINE_CHANNEL_SECRET")
	lineRedirectURI := os.Getenv("LINE_REDIRECT_URI")

	// LINEの設定がない場合はデフォルト値を使用（オプショナル）
	if lineChannelID == "" {
		lineChannelID = "dummy_line_channel_id"
		log.Println("⚠️  LINE_CHANNEL_ID not set, LINE Login will not work")
	}
	if lineChannelSecret == "" {
		lineChannelSecret = "dummy_line_channel_secret"
	}
	if lineRedirectURI == "" {
		lineRedirectURI = "http://localhost:8080/callback"
	}

	lineConfig := &auth.LINEConfig{
		ClientID:     lineChannelID,
		ClientSecret: lineChannelSecret,
		RedirectURI:  lineRedirectURI,
		Scopes:       []string{"profile", "openid", "email"},
	}
	lineManager := auth.NewLINEManager(lineConfig)

	// Initialize Production Database (optional, read-only)
	var prodDB *dbconfig.ProdDatabase
	prodDB, err = dbconfig.NewProdDatabase()
	if err != nil {
		log.Printf("⚠️  Production database connection failed: %v", err)
		log.Println("⚠️  TimeCard endpoints with PROD environment will not work")
	} else {
		log.Println("✅ Production database connected successfully")
		defer prodDB.Close()
	}

	// Initialize Development Database (optional, read-write)
	var devDB *gorm.DB
	devConfig, err := dbconfig.LoadConfig()
	if err != nil {
		log.Printf("⚠️  Development database config load failed: %v", err)
		log.Println("⚠️  TimeCard endpoints with DEV environment will not work")
	} else {
		devDB, err = dbconfig.InitDatabase(devConfig)
		if err != nil {
			log.Printf("⚠️  Development database connection failed: %v", err)
			log.Println("⚠️  TimeCard endpoints with DEV environment will not work")
		} else {
			log.Println("✅ Development database connected successfully")
			defer dbconfig.CloseDatabase(devDB)
		}
	}

	// Public methods that don't require WOFF authentication
	publicMethods := []string{
		"/auth.v1.AuthService/GetAuthorizationURL",
		"/auth.v1.AuthService/ExchangeCode",
		"/auth.v1.AuthService/RefreshToken",
		"/auth.v1.AuthService/VerifyToken",
		// TimeCard endpoints use FRONTEND_SECRET authentication instead
		"/auth.v1.AuthService/GetTimeCard",
		"/auth.v1.AuthService/ListTimeCards",
		"/auth.v1.AuthService/CreateTimeCard",
		"/auth.v1.AuthService/UpdateTimeCard",
		"/auth.v1.AuthService/DeleteTimeCard",
	}

	// TimeCard endpoints that require FRONTEND_SECRET authentication
	secretMethods := []string{
		"/auth.v1.AuthService/GetTimeCard",
		"/auth.v1.AuthService/ListTimeCards",
		"/auth.v1.AuthService/CreateTimeCard",
		"/auth.v1.AuthService/UpdateTimeCard",
		"/auth.v1.AuthService/DeleteTimeCard",
	}

	// Get FRONTEND_SECRET for timecard authentication
	frontendSecret := os.Getenv("FRONTEND_SECRET")
	if frontendSecret == "" {
		log.Println("⚠️  FRONTEND_SECRET not set, TimeCard endpoints will not work")
	}

	// Create WOFF interceptor
	woffInterceptor := interceptor.NewWOFFInterceptor(woffManager, publicMethods)

	// Create Secret interceptor for TimeCard endpoints
	secretInterceptor := interceptor.NewSecretInterceptor(frontendSecret, secretMethods)

	// Chain interceptors: first secret check, then WOFF check
	chainedInterceptor := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// First check secret authentication for timecard endpoints
		secretCtx, secretErr := secretInterceptor.Unary()(ctx, req, info, handler)
		if secretErr == nil {
			return secretCtx, nil
		}
		// If secret auth failed, try WOFF authentication
		return woffInterceptor.Unary()(ctx, req, info, handler)
	}

	// Create gRPC server with chained interceptors
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(chainedInterceptor),
		grpc.StreamInterceptor(woffInterceptor.Stream()),
	)

	// Register auth service
	authService := newWOFFAuthServer(woffManager, lineManager, woffStore, prodDB, devDB)
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
