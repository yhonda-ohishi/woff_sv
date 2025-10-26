package interceptor

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// SecretInterceptor is a gRPC interceptor for secret-based authentication
type SecretInterceptor struct {
	secret        string
	secretMethods map[string]bool
}

func NewSecretInterceptor(secret string, secretMethods []string) *SecretInterceptor {
	secretMethodsMap := make(map[string]bool)
	for _, method := range secretMethods {
		secretMethodsMap[method] = true
	}

	return &SecretInterceptor{
		secret:        secret,
		secretMethods: secretMethodsMap,
	}
}

// Unary returns a unary server interceptor for secret authentication
func (i *SecretInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Check if method requires secret authentication
		if !i.secretMethods[info.FullMethod] {
			return nil, status.Errorf(codes.PermissionDenied, "method does not use secret authentication")
		}

		// Validate secret
		if err := i.authorize(ctx); err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

// Stream returns a stream server interceptor for secret authentication
func (i *SecretInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// Check if method requires secret authentication
		if !i.secretMethods[info.FullMethod] {
			return status.Errorf(codes.PermissionDenied, "method does not use secret authentication")
		}

		// Validate secret
		if err := i.authorize(stream.Context()); err != nil {
			return err
		}

		return handler(srv, stream)
	}
}

func (i *SecretInterceptor) authorize(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Errorf(codes.Unauthenticated, "metadata is not provided")
	}

	values := md["x-api-secret"]
	if len(values) == 0 {
		// Also check authorization header as fallback
		values = md["authorization"]
		if len(values) == 0 {
			return status.Errorf(codes.Unauthenticated, "x-api-secret header is not provided")
		}
	}

	providedSecret := values[0]
	// Remove "Bearer " prefix if present
	if strings.HasPrefix(providedSecret, "Bearer ") {
		providedSecret = strings.TrimPrefix(providedSecret, "Bearer ")
	}

	// Verify secret
	if providedSecret != i.secret {
		return status.Errorf(codes.Unauthenticated, "invalid secret")
	}

	return nil
}
