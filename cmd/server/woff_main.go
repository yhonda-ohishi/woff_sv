package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/example/jwt-grpc-server/gen/auth/v1"
	"github.com/example/jwt-grpc-server/gen/auth/v1/authv1connect"
	"github.com/example/jwt-grpc-server/internal/auth"
	"github.com/example/jwt-grpc-server/internal/database"
	"github.com/example/jwt-grpc-server/internal/flickr"
	"github.com/example/jwt-grpc-server/internal/interceptor"
	"github.com/example/jwt-grpc-server/internal/registration"
	"github.com/example/jwt-grpc-server/internal/tunnel"
	"github.com/mrjones/oauth"
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

// convertWebMToMP4 converts a WebM file to MP4 using FFmpeg
func convertWebMToMP4(inputPath, outputPath string) error {
	log.Printf("Converting WebM to MP4: %s -> %s", inputPath, outputPath)

	// FFmpeg command: ffmpeg -i input.webm -c:v libx264 -c:a aac -strict experimental output.mp4
	cmd := exec.Command("ffmpeg",
		"-i", inputPath,
		"-c:v", "libx264",
		"-preset", "fast",
		"-c:a", "aac",
		"-strict", "experimental",
		"-y", // Overwrite output file if exists
		outputPath,
	)

	// Capture output for debugging
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("FFmpeg error: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("ffmpeg conversion failed: %w", err)
	}

	log.Printf("✅ Conversion successful: %s", outputPath)
	return nil
}

// mergeAndConvertToMP4 merges separate video and audio WebM files and converts to MP4
func mergeAndConvertToMP4(videoPath, audioPath, outputPath string) error {
	log.Printf("Merging and converting: video=%s, audio=%s -> %s", videoPath, audioPath, outputPath)

	// FFmpeg command: ffmpeg -i video.webm -i audio.webm -c:v libx264 -c:a aac -shortest output.mp4
	cmd := exec.Command("ffmpeg",
		"-i", videoPath, // Input video
		"-i", audioPath, // Input audio
		"-c:v", "libx264",
		"-preset", "fast",
		"-c:a", "aac",
		"-strict", "experimental",
		"-shortest", // Match shortest stream duration
		"-y",        // Overwrite output file if exists
		outputPath,
	)

	// Capture output for debugging
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("FFmpeg merge error: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("ffmpeg merge failed: %w", err)
	}

	log.Printf("✅ Merge and conversion successful: %s", outputPath)
	return nil
}

// handleRecordingUpload handles video recording uploads
func handleRecordingUpload(w http.ResponseWriter, r *http.Request, prodDB *dbconfig.ProdDatabase) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.Println("📹 Receiving recording upload...")

	// Parse multipart form (max 1GB for Flickr limit)
	if err := r.ParseMultipartForm(1024 * 1024 * 1024); err != nil {
		log.Printf("Failed to parse multipart form: %v", err)
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Extract form fields
	sessionId := r.FormValue("sessionId")
	timestamp := r.FormValue("timestamp")
	partNumber := r.FormValue("partNumber")
	streamType := r.FormValue("streamType") // "local" or "remote"

	// Default to empty if not provided (for backward compatibility)
	if streamType == "" {
		streamType = "unknown"
	}

	log.Printf("📹 Receiving recording upload...")
	log.Printf("Session: %s, Timestamp: %s, Part: %s, Type: %s", sessionId, timestamp, partNumber, streamType)

	// Create temp directory for processing
	tempDir := filepath.Join(os.TempDir(), "woff_recordings")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Printf("Failed to create temp directory: %v", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	// Get video file
	videoFile, videoHeader, err := r.FormFile("video")
	if err != nil {
		log.Printf("Failed to get video file: %v", err)
		http.Error(w, "Missing video file", http.StatusBadRequest)
		return
	}
	defer videoFile.Close()

	log.Printf("Received video: %s, size: %d bytes", videoHeader.Filename, videoHeader.Size)

	// Save video WebM file
	videoWebmPath := filepath.Join(tempDir, fmt.Sprintf("%s_%s_%s_part%s_video.webm", sessionId, streamType, timestamp, partNumber))
	videoWebmFile, err := os.Create(videoWebmPath)
	if err != nil {
		log.Printf("Failed to create video WebM file: %v", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	defer os.Remove(videoWebmPath) // Clean up temp file

	if _, err := io.Copy(videoWebmFile, videoFile); err != nil {
		videoWebmFile.Close()
		log.Printf("Failed to save video WebM file: %v", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	videoWebmFile.Close()

	// Check if audio file is provided (optional)
	audioFile, audioHeader, audioErr := r.FormFile("audio")
	var audioWebmPath string
	hasAudio := audioErr == nil

	if hasAudio {
		defer audioFile.Close()
		log.Printf("Received audio: %s, size: %d bytes", audioHeader.Filename, audioHeader.Size)

		// Save audio WebM file
		audioWebmPath = filepath.Join(tempDir, fmt.Sprintf("%s_%s_%s_part%s_audio.webm", sessionId, streamType, timestamp, partNumber))
		audioWebmFile, err := os.Create(audioWebmPath)
		if err != nil {
			log.Printf("Failed to create audio WebM file: %v", err)
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		defer os.Remove(audioWebmPath) // Clean up temp file

		if _, err := io.Copy(audioWebmFile, audioFile); err != nil {
			audioWebmFile.Close()
			log.Printf("Failed to save audio WebM file: %v", err)
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		audioWebmFile.Close()
	}

	// Convert to MP4 (merge audio and video if both exist)
	mp4Path := filepath.Join(tempDir, fmt.Sprintf("%s_%s_%s_part%s.mp4", sessionId, streamType, timestamp, partNumber))

	if hasAudio {
		// Merge video and audio then convert to MP4
		log.Printf("🎬 Merging video and audio streams...")
		if err := mergeAndConvertToMP4(videoWebmPath, audioWebmPath, mp4Path); err != nil {
			log.Printf("Failed to merge and convert: %v", err)
			http.Error(w, "Conversion failed", http.StatusInternalServerError)
			return
		}
	} else {
		// Convert video-only WebM to MP4
		log.Printf("🎬 Converting video-only WebM to MP4...")
		if err := convertWebMToMP4(videoWebmPath, mp4Path); err != nil {
			log.Printf("Failed to convert to MP4: %v", err)
			http.Error(w, "Conversion failed", http.StatusInternalServerError)
			return
		}
	}
	defer os.Remove(mp4Path) // Clean up temp file

	log.Printf("✅ Successfully converted to MP4: %s", mp4Path)

	// Upload MP4 to Flickr
	flickrAPIKey := os.Getenv("FLICKR_API_KEY")
	flickrAPISecret := os.Getenv("FLICKR_API_SECRET")

	if flickrAPIKey == "" || flickrAPISecret == "" {
		log.Println("⚠️  FLICKR_API_KEY or FLICKR_API_SECRET not set, skipping Flickr upload")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"success": true, "message": "Recording converted (Flickr upload skipped)", "recordingId": "%s_%s_part%s"}`, sessionId, streamType, partNumber)
		return
	}

	// Load tokens from file
	tokens, err := loadFlickrTokens()
	if err != nil {
		log.Printf("⚠️  Failed to load Flickr tokens: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"success": true, "message": "Recording converted (Flickr OAuth error)", "recordingId": "%s_%s_part%s", "authUrl": "http://localhost:50051/api/flickr/auth"}`, sessionId, streamType, partNumber)
		return
	}

	if tokens == nil || tokens.AccessToken == "" || tokens.AccessSecret == "" {
		log.Println("⚠️  Flickr tokens not found")
		log.Println("💡 Visit http://localhost:50051/api/flickr/auth to get OAuth tokens")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"success": true, "message": "Recording converted (Flickr OAuth required)", "recordingId": "%s_%s_part%s", "authUrl": "http://localhost:50051/api/flickr/auth"}`, sessionId, streamType, partNumber)
		return
	}

	log.Printf("✅ Loaded Flickr tokens from %s", flickrTokensFile)
	flickrClient := flickr.NewClientWithToken(flickrAPIKey, flickrAPISecret, tokens.AccessToken, tokens.AccessSecret)

	title := fmt.Sprintf("Recording %s (%s) - Part %s", sessionId, streamType, partNumber)
	description := fmt.Sprintf("Video call recording (%s stream) from session %s, part %s, timestamp %s", streamType, sessionId, partNumber, timestamp)

	log.Printf("📤 Uploading to Flickr: %s", title)
	photoID, err := flickrClient.UploadVideo(mp4Path, title, description, false)
	if err != nil {
		log.Printf("Failed to upload to Flickr: %v", err)
		http.Error(w, "Flickr upload failed", http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Successfully uploaded to Flickr with photo ID: %s", photoID)

	// TODO: Save metadata to database

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"success": true, "message": "Recording uploaded successfully", "recordingId": "%s_%s_part%s", "flickrPhotoId": "%s"}`, sessionId, streamType, partNumber, photoID)
}

// Global variable to store OAuth tokens temporarily
var flickrTokenStore = struct {
	sync.RWMutex
	requestToken       string
	requestTokenSecret string
	accessToken        string
	accessTokenSecret  string
}{}

const flickrTokensFile = "flickr_tokens.json"

type FlickrTokens struct {
	AccessToken  string `json:"access_token"`
	AccessSecret string `json:"access_secret"`
}

// saveFlickrTokens saves OAuth tokens to file
func saveFlickrTokens(accessToken, accessSecret string) error {
	tokens := FlickrTokens{
		AccessToken:  accessToken,
		AccessSecret: accessSecret,
	}

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tokens: %w", err)
	}

	err = os.WriteFile(flickrTokensFile, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to write tokens file: %w", err)
	}

	return nil
}

// loadFlickrTokens loads OAuth tokens from file
func loadFlickrTokens() (*FlickrTokens, error) {
	data, err := os.ReadFile(flickrTokensFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("tokens file not found")
		}
		return nil, fmt.Errorf("failed to read tokens file: %w", err)
	}

	var tokens FlickrTokens
	err = json.Unmarshal(data, &tokens)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal tokens: %w", err)
	}

	return &tokens, nil
}

// handleFlickrAuth initiates Flickr OAuth flow
func handleFlickrAuth(w http.ResponseWriter, r *http.Request) {
	apiKey := os.Getenv("FLICKR_API_KEY")
	apiSecret := os.Getenv("FLICKR_API_SECRET")

	if apiKey == "" || apiSecret == "" {
		http.Error(w, "Flickr API credentials not set", http.StatusInternalServerError)
		return
	}

	consumer := oauth.NewConsumer(
		apiKey,
		apiSecret,
		oauth.ServiceProvider{
			RequestTokenUrl:   "https://www.flickr.com/services/oauth/request_token",
			AuthorizeTokenUrl: "https://www.flickr.com/services/oauth/authorize",
			AccessTokenUrl:    "https://www.flickr.com/services/oauth/access_token",
		},
	)

	// Request OAuth token
	callbackURL := "http://localhost:50051/api/flickr/callback"
	requestTok, url, err := consumer.GetRequestTokenAndUrl(callbackURL)
	if err != nil {
		log.Printf("Failed to get request token: %v", err)
		http.Error(w, "Failed to get request token", http.StatusInternalServerError)
		return
	}

	// Add write permission to authorization URL (required for upload)
	url = url + "&perms=write"

	// Store request token
	flickrTokenStore.Lock()
	flickrTokenStore.requestToken = requestTok.Token
	flickrTokenStore.requestTokenSecret = requestTok.Secret
	flickrTokenStore.Unlock()

	log.Printf("🔑 Flickr authorization URL: %s", url)

	// Redirect to Flickr authorization page
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// handleFlickrCallback handles OAuth callback from Flickr
func handleFlickrCallback(w http.ResponseWriter, r *http.Request) {
	apiKey := os.Getenv("FLICKR_API_KEY")
	apiSecret := os.Getenv("FLICKR_API_SECRET")

	oauthToken := r.URL.Query().Get("oauth_token")
	oauthVerifier := r.URL.Query().Get("oauth_verifier")

	if oauthToken == "" || oauthVerifier == "" {
		http.Error(w, "Missing oauth parameters", http.StatusBadRequest)
		return
	}

	consumer := oauth.NewConsumer(
		apiKey,
		apiSecret,
		oauth.ServiceProvider{
			RequestTokenUrl:   "https://www.flickr.com/services/oauth/request_token",
			AuthorizeTokenUrl: "https://www.flickr.com/services/oauth/authorize",
			AccessTokenUrl:    "https://www.flickr.com/services/oauth/access_token",
		},
	)

	// Get stored request token
	flickrTokenStore.RLock()
	requestToken := flickrTokenStore.requestToken
	requestTokenSecret := flickrTokenStore.requestTokenSecret
	flickrTokenStore.RUnlock()

	if requestToken != oauthToken {
		http.Error(w, "OAuth token mismatch", http.StatusBadRequest)
		return
	}

	// Exchange for access token
	accessTok, err := consumer.AuthorizeToken(&oauth.RequestToken{
		Token:  requestToken,
		Secret: requestTokenSecret,
	}, oauthVerifier)
	if err != nil {
		log.Printf("Failed to get access token: %v", err)
		http.Error(w, "Failed to get access token", http.StatusInternalServerError)
		return
	}

	// Store access token in memory
	flickrTokenStore.Lock()
	flickrTokenStore.accessToken = accessTok.Token
	flickrTokenStore.accessTokenSecret = accessTok.Secret
	flickrTokenStore.Unlock()

	// Save to file
	err = saveFlickrTokens(accessTok.Token, accessTok.Secret)
	if err != nil {
		log.Printf("⚠️  Failed to save tokens to file: %v", err)
	} else {
		log.Printf("✅ Flickr tokens saved to %s", flickrTokensFile)
	}

	log.Printf("✅ Flickr access token obtained successfully")
	log.Printf("Access Token: %s", accessTok.Token)
	log.Printf("Access Secret: %s", accessTok.Secret)

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `
		<html>
		<body>
			<h2>Flickr Authentication Successful!</h2>
			<p>Tokens have been automatically saved to <code>%s</code></p>
			<p>Access Token: <code>%s</code></p>
			<p>Access Secret: <code>%s</code></p>
			<p>You can now close this window and upload videos!</p>
		</body>
		</html>
	`, flickrTokensFile, accessTok.Token, accessTok.Secret)
}

// handleFlickrGetToken returns current OAuth tokens (for testing)
func handleFlickrGetToken(w http.ResponseWriter, r *http.Request) {
	flickrTokenStore.RLock()
	defer flickrTokenStore.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"accessToken": "%s", "accessSecret": "%s"}`,
		flickrTokenStore.accessToken, flickrTokenStore.accessTokenSecret)
}

// handleTestFlickrUpload tests Flickr upload with a simple file
func handleTestFlickrUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Load Flickr tokens from file
	tokens, err := loadFlickrTokens()
	if err != nil {
		log.Printf("Failed to load Flickr tokens: %v", err)
		http.Error(w, "Failed to load tokens", http.StatusInternalServerError)
		return
	}

	if tokens == nil || tokens.AccessToken == "" || tokens.AccessSecret == "" {
		log.Println("⚠️  Flickr tokens not found")
		http.Error(w, "Flickr tokens not configured. Please visit /api/flickr/auth first", http.StatusUnauthorized)
		return
	}

	// Parse multipart form
	err = r.ParseMultipartForm(10 << 20) // 10 MB
	if err != nil {
		log.Printf("Failed to parse form: %v", err)
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get uploaded file
	file, _, err := r.FormFile("file")
	if err != nil {
		log.Printf("Failed to get file: %v", err)
		http.Error(w, "Failed to get file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Get form values
	title := r.FormValue("title")
	description := r.FormValue("description")

	// Save file to temp location
	tempFile, err := os.CreateTemp("", "flickr-test-*.jpg")
	if err != nil {
		log.Printf("Failed to create temp file: %v", err)
		http.Error(w, "Failed to create temp file", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		log.Printf("Failed to copy file: %v", err)
		http.Error(w, "Failed to copy file", http.StatusInternalServerError)
		return
	}

	// Upload to Flickr
	flickrAPIKey := os.Getenv("FLICKR_API_KEY")
	flickrAPISecret := os.Getenv("FLICKR_API_SECRET")
	flickrClient := flickr.NewClientWithToken(flickrAPIKey, flickrAPISecret, tokens.AccessToken, tokens.AccessSecret)

	log.Printf("🧪 Testing Flickr upload: %s", title)
	photoID, err := flickrClient.UploadVideo(tempFile.Name(), title, description, false)
	if err != nil {
		log.Printf("❌ Upload failed: %v", err)
		http.Error(w, fmt.Sprintf("Upload failed: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Upload successful! Photo ID: %s", photoID)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success": true, "photoId": "%s"}`, photoID)
}

type woffAuthServer struct {
	authv1.UnimplementedAuthServiceServer
	woffManager          *auth.WOFFManager
	lineManager          *auth.LINEManager
	woffStore            *database.WOFFStore
	responseCache        sync.Map // codeをキーにしてレスポンスをキャッシュ
	processingCode       sync.Map // 処理中のcodeを追跡
	prodDB               *dbconfig.ProdDatabase
	devDB                *gorm.DB
	timeCardProdRepo     repository.TimeCardRepository
	timeCardDevRepo      repository.TimeCardDevRepository
	timeCardLogRepo      repository.TimeCardLogRepository
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
	var timeCardLogRepo repository.TimeCardLogRepository

	if prodDB != nil {
		timeCardProdRepo = repository.NewTimeCardRepository(prodDB)
	}
	if devDB != nil {
		timeCardDevRepo = repository.NewTimeCardDevRepository(devDB)
		timeCardLogRepo = repository.NewTimeCardLogRepository(devDB)
	}

	return &woffAuthServer{
		woffManager:      woffManager,
		lineManager:      lineManager,
		woffStore:        woffStore,
		prodDB:           prodDB,
		devDB:            devDB,
		timeCardProdRepo: timeCardProdRepo,
		timeCardDevRepo:  timeCardDevRepo,
		timeCardLogRepo:  timeCardLogRepo,
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

func (s *connectAuthServer) Heartbeat(ctx context.Context, req *connect.Request[authv1.HeartbeatRequest]) (*connect.Response[authv1.HeartbeatResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/Heartbeat")
	resp, err := s.grpcServer.Heartbeat(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
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

func (s *connectAuthServer) GetTimeCardLog(ctx context.Context, req *connect.Request[authv1.GetTimeCardLogRequest]) (*connect.Response[authv1.TimeCardLogResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/GetTimeCardLog")
	resp, err := s.grpcServer.GetTimeCardLog(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) ListTimeCardLogs(ctx context.Context, req *connect.Request[authv1.ListTimeCardLogsRequest]) (*connect.Response[authv1.ListTimeCardLogsResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/ListTimeCardLogs")
	resp, err := s.grpcServer.ListTimeCardLogs(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) ListTimeCardLogsByCardID(ctx context.Context, req *connect.Request[authv1.ListTimeCardLogsByCardIDRequest]) (*connect.Response[authv1.ListTimeCardLogsResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/ListTimeCardLogsByCardID")
	resp, err := s.grpcServer.ListTimeCardLogsByCardID(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) CreateTimeCardLog(ctx context.Context, req *connect.Request[authv1.CreateTimeCardLogRequest]) (*connect.Response[authv1.TimeCardLogResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/CreateTimeCardLog")
	resp, err := s.grpcServer.CreateTimeCardLog(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) UpdateTimeCardLog(ctx context.Context, req *connect.Request[authv1.UpdateTimeCardLogRequest]) (*connect.Response[authv1.TimeCardLogResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/UpdateTimeCardLog")
	resp, err := s.grpcServer.UpdateTimeCardLog(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *connectAuthServer) DeleteTimeCardLog(ctx context.Context, req *connect.Request[authv1.DeleteTimeCardLogRequest]) (*connect.Response[authv1.DeleteTimeCardLogResponse], error) {
	log.Printf("Connect request: /auth.v1.AuthService/DeleteTimeCardLog")
	resp, err := s.grpcServer.DeleteTimeCardLog(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *woffAuthServer) Heartbeat(ctx context.Context, req *authv1.HeartbeatRequest) (*authv1.HeartbeatResponse, error) {
	log.Printf("Heartbeat request received")

	return &authv1.HeartbeatResponse{
		Status:    "ok",
		Timestamp: time.Now().Format(time.RFC3339),
		Version:   "1.0.0",
	}, nil
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

// GetTimeCardLog retrieves a timecard log by composite key
func (s *woffAuthServer) GetTimeCardLog(ctx context.Context, req *authv1.GetTimeCardLogRequest) (*authv1.TimeCardLogResponse, error) {
	log.Printf("GetTimeCardLog request: environment=%v, datetime=%s, id=%d", req.Environment, req.Datetime, req.Id)

	// Only dev environment is supported for timecard_logs
	if s.timeCardLogRepo == nil {
		return nil, status.Error(codes.Unavailable, "timecard log repository not available")
	}

	// Get from database (datetime is stored as string in TimeCardLog)
	timeCardLog, err := s.timeCardLogRepo.GetByCompositeKey(req.Datetime, int(req.Id))
	if err != nil {
		log.Printf("Failed to get timecard log: %v", err)
		return nil, status.Errorf(codes.NotFound, "timecard log not found: %v", err)
	}

	// Convert to proto message
	stateDetail := ""
	if timeCardLog.StateDetail != nil {
		stateDetail = *timeCardLog.StateDetail
	}

	return &authv1.TimeCardLogResponse{
		Log: &authv1.TimeCardLog{
			Datetime:    timeCardLog.Datetime,
			Id:          int32(timeCardLog.ID),
			CardId:      timeCardLog.CardID,
			MachineIp:   timeCardLog.MachineIP,
			State:       timeCardLog.State,
			StateDetail: stateDetail,
			Created:     timeCardLog.Created.Format(time.RFC3339),
			Modified:    timeCardLog.Modified.Format(time.RFC3339),
		},
	}, nil
}

// ListTimeCardLogs retrieves a list of timecard logs
func (s *woffAuthServer) ListTimeCardLogs(ctx context.Context, req *authv1.ListTimeCardLogsRequest) (*authv1.ListTimeCardLogsResponse, error) {
	log.Printf("ListTimeCardLogs request: environment=%v, limit=%d, offset=%d", req.Environment, req.Limit, req.Offset)

	if s.timeCardLogRepo == nil {
		return nil, status.Error(codes.Unavailable, "timecard log repository not available")
	}

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

	// Get from database
	logs, totalCount, err := s.timeCardLogRepo.GetAll(limit, offset, orderBy)
	if err != nil {
		log.Printf("Failed to list timecard logs: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to list timecard logs: %v", err)
	}

	// Convert to proto messages
	protoLogs := make([]*authv1.TimeCardLog, 0, len(logs))
	for _, l := range logs {
		stateDetail := ""
		if l.StateDetail != nil {
			stateDetail = *l.StateDetail
		}

		protoLogs = append(protoLogs, &authv1.TimeCardLog{
			Datetime:    l.Datetime,
			Id:          int32(l.ID),
			CardId:      l.CardID,
			MachineIp:   l.MachineIP,
			State:       l.State,
			StateDetail: stateDetail,
			Created:     l.Created.Format(time.RFC3339),
			Modified:    l.Modified.Format(time.RFC3339),
		})
	}

	return &authv1.ListTimeCardLogsResponse{
		Logs:       protoLogs,
		TotalCount: totalCount,
	}, nil
}

// ListTimeCardLogsByCardID retrieves timecard logs by card_id
func (s *woffAuthServer) ListTimeCardLogsByCardID(ctx context.Context, req *authv1.ListTimeCardLogsByCardIDRequest) (*authv1.ListTimeCardLogsResponse, error) {
	log.Printf("ListTimeCardLogsByCardID request: card_id=%s, limit=%d, offset=%d", req.CardId, req.Limit, req.Offset)

	if s.timeCardLogRepo == nil {
		return nil, status.Error(codes.Unavailable, "timecard log repository not available")
	}

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

	// Get from database (GetByCardID does not take orderBy parameter)
	logs, totalCount, err := s.timeCardLogRepo.GetByCardID(req.CardId, limit, offset)
	if err != nil {
		log.Printf("Failed to list timecard logs by card_id: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to list timecard logs: %v", err)
	}

	// Convert to proto messages
	protoLogs := make([]*authv1.TimeCardLog, 0, len(logs))
	for _, l := range logs {
		stateDetail := ""
		if l.StateDetail != nil {
			stateDetail = *l.StateDetail
		}

		protoLogs = append(protoLogs, &authv1.TimeCardLog{
			Datetime:    l.Datetime,
			Id:          int32(l.ID),
			CardId:      l.CardID,
			MachineIp:   l.MachineIP,
			State:       l.State,
			StateDetail: stateDetail,
			Created:     l.Created.Format(time.RFC3339),
			Modified:    l.Modified.Format(time.RFC3339),
		})
	}

	return &authv1.ListTimeCardLogsResponse{
		Logs:       protoLogs,
		TotalCount: totalCount,
	}, nil
}

// CreateTimeCardLog creates a new timecard log
func (s *woffAuthServer) CreateTimeCardLog(ctx context.Context, req *authv1.CreateTimeCardLogRequest) (*authv1.TimeCardLogResponse, error) {
	log.Printf("CreateTimeCardLog request: datetime=%s, id=%d, card_id=%s", req.Datetime, req.Id, req.CardId)

	if s.timeCardLogRepo == nil {
		return nil, status.Error(codes.Unavailable, "timecard log repository not available")
	}

	// Create timecard log model (datetime is stored as string)
	now := time.Now()
	var stateDetail *string
	if req.StateDetail != "" {
		stateDetail = &req.StateDetail
	}

	timeCardLog := &mysql.TimeCardLog{
		Datetime:    req.Datetime,
		ID:          int(req.Id),
		CardID:      req.CardId,
		MachineIP:   req.MachineIp,
		State:       req.State,
		StateDetail: stateDetail,
		Created:     now,
		Modified:    now,
	}

	// Create in database
	if err := s.timeCardLogRepo.Create(timeCardLog); err != nil {
		log.Printf("Failed to create timecard log: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to create timecard log: %v", err)
	}

	log.Printf("✅ Successfully created timecard log: datetime=%s, id=%d", req.Datetime, req.Id)

	// Return created log
	resultStateDetail := ""
	if timeCardLog.StateDetail != nil {
		resultStateDetail = *timeCardLog.StateDetail
	}

	return &authv1.TimeCardLogResponse{
		Log: &authv1.TimeCardLog{
			Datetime:    timeCardLog.Datetime,
			Id:          int32(timeCardLog.ID),
			CardId:      timeCardLog.CardID,
			MachineIp:   timeCardLog.MachineIP,
			State:       timeCardLog.State,
			StateDetail: resultStateDetail,
			Created:     timeCardLog.Created.Format(time.RFC3339),
			Modified:    timeCardLog.Modified.Format(time.RFC3339),
		},
	}, nil
}

// UpdateTimeCardLog updates an existing timecard log
func (s *woffAuthServer) UpdateTimeCardLog(ctx context.Context, req *authv1.UpdateTimeCardLogRequest) (*authv1.TimeCardLogResponse, error) {
	log.Printf("UpdateTimeCardLog request: datetime=%s, id=%d", req.Datetime, req.Id)

	if s.timeCardLogRepo == nil {
		return nil, status.Error(codes.Unavailable, "timecard log repository not available")
	}

	// Get existing log (datetime is stored as string)
	existingLog, err := s.timeCardLogRepo.GetByCompositeKey(req.Datetime, int(req.Id))
	if err != nil {
		log.Printf("Failed to find timecard log: %v", err)
		return nil, status.Errorf(codes.NotFound, "timecard log not found: %v", err)
	}

	// Update fields
	existingLog.CardID = req.CardId
	existingLog.MachineIP = req.MachineIp
	existingLog.State = req.State
	if req.StateDetail != "" {
		existingLog.StateDetail = &req.StateDetail
	} else {
		existingLog.StateDetail = nil
	}
	existingLog.Modified = time.Now()

	// Update in database
	if err := s.timeCardLogRepo.Update(existingLog); err != nil {
		log.Printf("Failed to update timecard log: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to update timecard log: %v", err)
	}

	log.Printf("✅ Successfully updated timecard log: datetime=%s, id=%d", req.Datetime, req.Id)

	// Return updated log
	resultStateDetail := ""
	if existingLog.StateDetail != nil {
		resultStateDetail = *existingLog.StateDetail
	}

	return &authv1.TimeCardLogResponse{
		Log: &authv1.TimeCardLog{
			Datetime:    existingLog.Datetime,
			Id:          int32(existingLog.ID),
			CardId:      existingLog.CardID,
			MachineIp:   existingLog.MachineIP,
			State:       existingLog.State,
			StateDetail: resultStateDetail,
			Created:     existingLog.Created.Format(time.RFC3339),
			Modified:    existingLog.Modified.Format(time.RFC3339),
		},
	}, nil
}

// DeleteTimeCardLog deletes a timecard log
func (s *woffAuthServer) DeleteTimeCardLog(ctx context.Context, req *authv1.DeleteTimeCardLogRequest) (*authv1.DeleteTimeCardLogResponse, error) {
	log.Printf("DeleteTimeCardLog request: datetime=%s, id=%d", req.Datetime, req.Id)

	if s.timeCardLogRepo == nil {
		return nil, status.Error(codes.Unavailable, "timecard log repository not available")
	}

	// Delete from database (datetime is stored as string)
	if err := s.timeCardLogRepo.Delete(req.Datetime, int(req.Id)); err != nil {
		log.Printf("Failed to delete timecard log: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to delete timecard log: %v", err)
	}

	log.Printf("✅ Successfully deleted timecard log: datetime=%s, id=%d", req.Datetime, req.Id)

	return &authv1.DeleteTimeCardLogResponse{
		Success: true,
		Message: "Timecard log deleted successfully",
	}, nil
}

func main() {
	// Kill any existing woff_server.exe processes on Windows (except current process)
	if runtime.GOOS == "windows" {
		log.Println("🔍 Checking for existing woff_server processes...")

		// Get current process ID
		currentPID := os.Getpid()

		// Find all woff_server.exe processes
		cmd := exec.Command("tasklist", "/FI", "IMAGENAME eq woff_server.exe", "/FO", "CSV", "/NH")
		output, err := cmd.CombinedOutput()

		if err == nil && len(output) > 0 {
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.Contains(line, "woff_server.exe") {
					// Parse PID from CSV format: "woff_server.exe","PID",...
					fields := strings.Split(line, ",")
					if len(fields) >= 2 {
						pidStr := strings.Trim(fields[1], "\" ")
						var pid int
						fmt.Sscanf(pidStr, "%d", &pid)

						// Kill only if it's not the current process
						if pid != currentPID && pid > 0 {
							killCmd := exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid))
							killCmd.Run()
							log.Printf("✅ Killed existing woff_server process (PID: %d)", pid)
							time.Sleep(2 * time.Second) // Wait for port to be released
						}
					}
				}
			}
		} else {
			log.Println("ℹ️  No existing woff_server process found")
		}
	}

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
		log.Println("⚠️  WOFF_CLIENT_ID and WOFF_CLIENT_SECRET not set")
		log.Println("⚠️  WOFF authentication will not work")
		log.Println("💡 Tip: Set WOFF_CLIENT_ID and WOFF_CLIENT_SECRET in .env file")
		// Set dummy values to allow server to start
		clientID = "dummy_client_id"
		clientSecret = "dummy_client_secret"
	}

	if redirectURI == "" {
		redirectURI = "http://localhost:8080/callback"
		log.Printf("WOFF_REDIRECT_URI not set, using default: %s", redirectURI)
	}

	if dbPath == "" {
		dbPath = "woff.db"
		log.Printf("DATABASE_PATH not set, using default: %s", dbPath)
	}

	// Check Flickr configuration status
	log.Println("")
	log.Println("📸 Flickr Integration Status:")
	flickrAPIKey := os.Getenv("FLICKR_API_KEY")
	flickrAPISecret := os.Getenv("FLICKR_API_SECRET")

	if flickrAPIKey == "" || flickrAPISecret == "" {
		log.Println("   ❌ API Keys: Not configured")
		log.Println("   💡 Set FLICKR_API_KEY and FLICKR_API_SECRET in .env file to enable Flickr uploads")
		log.Println("   ℹ️  Recordings will be converted to MP4 but not uploaded to Flickr")
	} else {
		log.Println("   ✅ API Keys: Configured")

		// Check if OAuth tokens exist
		if _, err := loadFlickrTokens(); err != nil {
			log.Println("   ⚠️  OAuth Tokens: Not authenticated")
			log.Println("")
			log.Println("   📋 To enable Flickr uploads, follow these steps:")
			log.Println("      1. Open your browser and visit:")
			log.Println("         http://localhost:50051/api/flickr/auth")
			log.Println("      2. Click 'Authorize' to grant permissions to Flickr")
			log.Println("      3. After authorization, tokens will be saved automatically")
			log.Println("      4. Restart the server to activate Flickr uploads")
			log.Println("")
			log.Println("   ℹ️  Until then: Recordings will be converted to MP4 but not uploaded")
		} else {
			log.Println("   ✅ OAuth Tokens: Ready")
			log.Println("   ℹ️  Flickr uploads enabled")
		}
	}
	log.Println("")

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
		prodDB = nil // Ensure it's nil on failure
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
		devDB = nil // Ensure it's nil on failure
	} else {
		devDB, err = dbconfig.InitDatabase(devConfig)
		if err != nil {
			log.Printf("⚠️  Development database connection failed: %v", err)
			log.Println("⚠️  TimeCard endpoints with DEV environment will not work")
			devDB = nil // Ensure it's nil on failure
		} else {
			log.Println("✅ Development database connected successfully")
			defer dbconfig.CloseDatabase(devDB)
		}
	}

	// Public methods that don't require WOFF authentication
	publicMethods := []string{
		"/auth.v1.AuthService/Heartbeat",
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
		// TimeCardLog endpoints use FRONTEND_SECRET authentication instead
		"/auth.v1.AuthService/GetTimeCardLog",
		"/auth.v1.AuthService/ListTimeCardLogs",
		"/auth.v1.AuthService/ListTimeCardLogsByCardID",
		"/auth.v1.AuthService/CreateTimeCardLog",
		"/auth.v1.AuthService/UpdateTimeCardLog",
		"/auth.v1.AuthService/DeleteTimeCardLog",
	}

	// TimeCard and TimeCardLog endpoints that require FRONTEND_SECRET authentication
	secretMethods := []string{
		"/auth.v1.AuthService/GetTimeCard",
		"/auth.v1.AuthService/ListTimeCards",
		"/auth.v1.AuthService/CreateTimeCard",
		"/auth.v1.AuthService/UpdateTimeCard",
		"/auth.v1.AuthService/DeleteTimeCard",
		"/auth.v1.AuthService/GetTimeCardLog",
		"/auth.v1.AuthService/ListTimeCardLogs",
		"/auth.v1.AuthService/ListTimeCardLogsByCardID",
		"/auth.v1.AuthService/CreateTimeCardLog",
		"/auth.v1.AuthService/UpdateTimeCardLog",
		"/auth.v1.AuthService/DeleteTimeCardLog",
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
	_, handler := authv1connect.NewAuthServiceHandler(
		connectService,
		connect.WithInterceptors(connect.UnaryInterceptorFunc(connectInterceptor)),
	)

	// Add recording upload endpoint
	mux.HandleFunc("/api/recordings/upload", corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleRecordingUpload(w, r, prodDB)
	})).ServeHTTP)

	// Add Flickr OAuth endpoints
	mux.HandleFunc("/api/flickr/auth", handleFlickrAuth)
	mux.HandleFunc("/api/flickr/callback", handleFlickrCallback)
	mux.HandleFunc("/api/flickr/token", handleFlickrGetToken)

	// Add Flickr test upload endpoint
	mux.HandleFunc("/api/test-flickr-upload", handleTestFlickrUpload)

	// Add CORS middleware
	// Use "/" to match all paths and let the handler's internal routing handle the path matching
	mux.Handle("/", corsMiddleware(handler))

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
					// Initial registration with retry
					if err := registrar.RegisterWithRetry(publicURL, 3); err != nil {
						log.Printf("❌ Failed initial registration with frontend: %v", err)
						log.Printf("🔄 Will continue retrying via WebSocket connection...")
					}

					// Maintain WebSocket connection for real-time updates and auto-reconnect
					registrar.MaintainConnection(publicURL)
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
