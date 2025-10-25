package interceptor

import (
	"context"
	"strings"

	"github.com/example/jwt-grpc-server/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type contextKey string

const (
	UserClaimsKey contextKey = "userClaims"
)

// AuthInterceptor is a gRPC interceptor for JWT authentication
type AuthInterceptor struct {
	jwtManager      *auth.JWTManager
	publicMethods   map[string]bool
}

func NewAuthInterceptor(jwtManager *auth.JWTManager, publicMethods []string) *AuthInterceptor {
	publicMethodsMap := make(map[string]bool)
	for _, method := range publicMethods {
		publicMethodsMap[method] = true
	}

	return &AuthInterceptor{
		jwtManager:    jwtManager,
		publicMethods: publicMethodsMap,
	}
}

// Unary returns a unary server interceptor for JWT authentication
func (i *AuthInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Check if method is public
		if i.publicMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		// Extract and validate token
		claims, err := i.authorize(ctx)
		if err != nil {
			return nil, err
		}

		// Add claims to context
		ctx = context.WithValue(ctx, UserClaimsKey, claims)

		return handler(ctx, req)
	}
}

// Stream returns a stream server interceptor for JWT authentication
func (i *AuthInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// Check if method is public
		if i.publicMethods[info.FullMethod] {
			return handler(srv, stream)
		}

		// Extract and validate token
		claims, err := i.authorize(stream.Context())
		if err != nil {
			return err
		}

		// Wrap stream with context containing claims
		wrappedStream := &wrappedStream{
			ServerStream: stream,
			ctx:          context.WithValue(stream.Context(), UserClaimsKey, claims),
		}

		return handler(srv, wrappedStream)
	}
}

func (i *AuthInterceptor) authorize(ctx context.Context) (*auth.JWTClaims, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "metadata is not provided")
	}

	values := md["authorization"]
	if len(values) == 0 {
		return nil, status.Errorf(codes.Unauthenticated, "authorization token is not provided")
	}

	accessToken := values[0]
	// Remove "Bearer " prefix if present
	if strings.HasPrefix(accessToken, "Bearer ") {
		accessToken = strings.TrimPrefix(accessToken, "Bearer ")
	}

	claims, err := i.jwtManager.ValidateToken(accessToken)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
	}

	return claims, nil
}

// GetClaimsFromContext retrieves JWT claims from context
func GetClaimsFromContext(ctx context.Context) (*auth.JWTClaims, bool) {
	claims, ok := ctx.Value(UserClaimsKey).(*auth.JWTClaims)
	return claims, ok
}

// wrappedStream wraps grpc.ServerStream with a custom context
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}
