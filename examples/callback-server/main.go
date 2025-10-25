package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	authv1 "github.com/example/jwt-grpc-server/gen/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	grpcServerAddr = "localhost:50051"
	callbackPort   = 8080
)

var (
	grpcClient authv1.AuthServiceClient
	grpcConn   *grpc.ClientConn
)

// HTML templates
const loginPageHTML = `
<!DOCTYPE html>
<html>
<head>
    <title>WOFF Login Demo</title>
    <style>
        body {
            font-family: Arial, sans-serif;
            max-width: 800px;
            margin: 50px auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background: white;
            padding: 30px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        h1 {
            color: #333;
        }
        button {
            background-color: #06c755;
            color: white;
            padding: 12px 24px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 16px;
        }
        button:hover {
            background-color: #05b34a;
        }
        .info {
            background-color: #e7f3ff;
            padding: 15px;
            border-left: 4px solid #2196F3;
            margin: 20px 0;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>🔐 WOFF Login Demo</h1>
        <div class="info">
            <p><strong>このデモについて:</strong></p>
            <p>LINE WORKS OAuth 2.0を使用した認証フローのデモンストレーションです。</p>
            <p>「LINE WORKSでログイン」ボタンをクリックして、認証を開始してください。</p>
        </div>
        <button onclick="startLogin()">LINE WORKSでログイン</button>
    </div>
    <script>
        function startLogin() {
            window.location.href = '/auth/start';
        }
    </script>
</body>
</html>
`

const profilePageHTML = `
<!DOCTYPE html>
<html>
<head>
    <title>User Profile - WOFF Login Demo</title>
    <style>
        body {
            font-family: Arial, sans-serif;
            max-width: 800px;
            margin: 50px auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background: white;
            padding: 30px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        h1 {
            color: #333;
        }
        .profile-item {
            margin: 15px 0;
            padding: 10px;
            background-color: #f9f9f9;
            border-left: 3px solid #06c755;
        }
        .profile-item strong {
            display: inline-block;
            width: 150px;
            color: #666;
        }
        .success {
            background-color: #d4edda;
            color: #155724;
            padding: 15px;
            border-radius: 4px;
            margin: 20px 0;
        }
        button {
            background-color: #6c757d;
            color: white;
            padding: 10px 20px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            margin-top: 20px;
        }
        button:hover {
            background-color: #5a6268;
        }
        .profile-image {
            width: 100px;
            height: 100px;
            border-radius: 50%;
            object-fit: cover;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>✅ ログイン成功</h1>
        <div class="success">
            LINE WORKS認証が正常に完了しました。
        </div>

        <h2>ユーザープロフィール</h2>
        {{if .ProfileImageUrl}}
        <img src="{{.ProfileImageUrl}}" alt="Profile" class="profile-image">
        {{end}}

        <div class="profile-item">
            <strong>User ID:</strong> {{.UserId}}
        </div>
        <div class="profile-item">
            <strong>Username:</strong> {{.UserName}}
        </div>
        <div class="profile-item">
            <strong>Display Name:</strong> {{.DisplayName}}
        </div>
        <div class="profile-item">
            <strong>Email:</strong> {{.Email}}
        </div>
        <div class="profile-item">
            <strong>Domain ID:</strong> {{.DomainId}}
        </div>
        <div class="profile-item">
            <strong>Roles:</strong> {{range .Roles}}{{.}} {{end}}
        </div>

        <h2>アクセストークン</h2>
        <div class="profile-item">
            <strong>Token:</strong> {{.AccessToken}}...
        </div>
        <div class="profile-item">
            <strong>Expires In:</strong> {{.ExpiresIn}} seconds
        </div>

        <button onclick="window.location.href='/'">トップに戻る</button>
    </div>
</body>
</html>
`

const errorPageHTML = `
<!DOCTYPE html>
<html>
<head>
    <title>Error - WOFF Login Demo</title>
    <style>
        body {
            font-family: Arial, sans-serif;
            max-width: 800px;
            margin: 50px auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background: white;
            padding: 30px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        .error {
            background-color: #f8d7da;
            color: #721c24;
            padding: 15px;
            border-radius: 4px;
            margin: 20px 0;
        }
        button {
            background-color: #6c757d;
            color: white;
            padding: 10px 20px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>❌ エラーが発生しました</h1>
        <div class="error">
            <strong>エラー:</strong> {{.Error}}
        </div>
        <button onclick="window.location.href='/'">トップに戻る</button>
    </div>
</body>
</html>
`

type ProfileData struct {
	UserId          string
	UserName        string
	DisplayName     string
	Email           string
	DomainId        string
	Roles           []string
	ProfileImageUrl string
	AccessToken     string
	ExpiresIn       int64
}

func init() {
	// Connect to gRPC server
	var err error
	grpcConn, err = grpc.NewClient(
		grpcServerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect to gRPC server: %v", err)
	}

	grpcClient = authv1.NewAuthServiceClient(grpcConn)
}

func main() {
	defer grpcConn.Close()

	// Routes
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/auth/start", handleAuthStart)
	http.HandleFunc("/callback", handleCallback)

	addr := fmt.Sprintf(":%d", callbackPort)
	log.Printf("Callback server listening on http://localhost%s", addr)
	log.Printf("Open your browser and navigate to: http://localhost%s", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.New("login").Parse(loginPageHTML)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

func handleAuthStart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get authorization URL from gRPC server
	resp, err := grpcClient.GetAuthorizationURL(ctx, &authv1.GetAuthorizationURLRequest{
		RedirectUri: fmt.Sprintf("http://localhost:%d/callback", callbackPort),
		Scopes:      []string{"user", "user.read"},
	})
	if err != nil {
		showError(w, fmt.Sprintf("Failed to get authorization URL: %v", err))
		return
	}

	// Store state in cookie for verification
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    resp.State,
		Path:     "/",
		MaxAge:   600, // 10 minutes
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// Redirect to WOFF authorization URL
	http.Redirect(w, r, resp.AuthorizationUrl, http.StatusFound)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	// Get code and state from query parameters
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" {
		errorMsg := r.URL.Query().Get("error")
		if errorMsg == "" {
			errorMsg = "No authorization code received"
		}
		showError(w, errorMsg)
		return
	}

	// Verify state
	cookie, err := r.Cookie("oauth_state")
	if err != nil || cookie.Value != state {
		showError(w, "Invalid state parameter (CSRF protection)")
		return
	}

	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Exchange code for tokens
	tokenResp, err := grpcClient.ExchangeCode(ctx, &authv1.ExchangeCodeRequest{
		Code:        code,
		RedirectUri: fmt.Sprintf("http://localhost:%d/callback", callbackPort),
		State:       state,
	})
	if err != nil {
		showError(w, fmt.Sprintf("Failed to exchange code: %v", err))
		return
	}

	// Get user profile
	md := metadata.New(map[string]string{
		"authorization": "Bearer " + tokenResp.AccessToken,
	})
	ctx = metadata.NewOutgoingContext(ctx, md)

	profileResp, err := grpcClient.GetProfile(ctx, &authv1.GetProfileRequest{})
	if err != nil {
		showError(w, fmt.Sprintf("Failed to get profile: %v", err))
		return
	}

	// Display profile
	showProfile(w, &ProfileData{
		UserId:          profileResp.UserId,
		UserName:        profileResp.UserName,
		DisplayName:     profileResp.DisplayName,
		Email:           profileResp.Email,
		DomainId:        profileResp.DomainId,
		Roles:           profileResp.Roles,
		ProfileImageUrl: profileResp.ProfileImageUrl,
		AccessToken:     tokenResp.AccessToken[:50], // Show first 50 chars
		ExpiresIn:       tokenResp.ExpiresIn,
	})
}

func showProfile(w http.ResponseWriter, data *ProfileData) {
	tmpl, err := template.New("profile").Parse(profilePageHTML)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, data)
}

func showError(w http.ResponseWriter, errorMsg string) {
	log.Printf("Error: %s", errorMsg)
	tmpl, err := template.New("error").Parse(errorPageHTML)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, map[string]string{"Error": errorMsg})
}
