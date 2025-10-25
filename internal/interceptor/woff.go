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

type woffContextKey string

const (
	WOFFUserInfoKey woffContextKey = "woffUserInfo"
)

// WOFFInterceptor is a gRPC interceptor for WOFF authentication
type WOFFInterceptor struct {
	woffManager   *auth.WOFFManager
	publicMethods map[string]bool
}

func NewWOFFInterceptor(woffManager *auth.WOFFManager, publicMethods []string) *WOFFInterceptor {
	publicMethodsMap := make(map[string]bool)
	for _, method := range publicMethods {
		publicMethodsMap[method] = true
	}

	return &WOFFInterceptor{
		woffManager:   woffManager,
		publicMethods: publicMethodsMap,
	}
}

// Unary returns a unary server interceptor for WOFF authentication
func (i *WOFFInterceptor) Unary() grpc.UnaryServerInterceptor {
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

		// Extract and validate WOFF token
		userInfo, err := i.authorize(ctx)
		if err != nil {
			return nil, err
		}

		// Add user info to context
		ctx = context.WithValue(ctx, WOFFUserInfoKey, userInfo)

		return handler(ctx, req)
	}
}

// Stream returns a stream server interceptor for WOFF authentication
func (i *WOFFInterceptor) Stream() grpc.StreamServerInterceptor {
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

		// Extract and validate WOFF token
		userInfo, err := i.authorize(stream.Context())
		if err != nil {
			return err
		}

		// Wrap stream with context containing user info
		wrappedStream := &woffWrappedStream{
			ServerStream: stream,
			ctx:          context.WithValue(stream.Context(), WOFFUserInfoKey, userInfo),
		}

		return handler(srv, wrappedStream)
	}
}

func (i *WOFFInterceptor) authorize(ctx context.Context) (*auth.WOFFUserInfo, error) {
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

	// Verify token and get user info
	userInfo, err := i.woffManager.VerifyToken(ctx, accessToken)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid WOFF token: %v", err)
	}

	return userInfo, nil
}

// GetWOFFUserInfoFromContext retrieves WOFF user info from context
func GetWOFFUserInfoFromContext(ctx context.Context) (*auth.WOFFUserInfo, bool) {
	userInfo, ok := ctx.Value(WOFFUserInfoKey).(*auth.WOFFUserInfo)
	return userInfo, ok
}

// woffWrappedStream wraps grpc.ServerStream with a custom context
type woffWrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *woffWrappedStream) Context() context.Context {
	return w.ctx
}
