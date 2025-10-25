# gRPC Authentication Server with Buf Schema Registry (BSR)

GoでWOFF (LINE WORKS) OAuth 2.0認証とJWT認証を実装したgRPCサーバーのプロジェクトです。Buf Schema Registry (BSR)を使用してフロントエンドとの統合を簡単にします。

## 🎯 2つの認証方式

このプロジェクトは2つの認証方式をサポートしています:

### 1. **WOFF Login (推奨)** - LINE WORKS OAuth 2.0
- ✅ エンタープライズグレードのOAuth 2.0認証
- ✅ LINE WORKSアカウントでシングルサインオン
- ✅ 認証コードフロー（Authorization Code Flow）
- ✅ トークンリフレッシュ・検証
- ✅ 本番環境推奨

**👉 [WOFF_README.md](WOFF_README.md) - 詳細ドキュメント**

### 2. **JWT Authentication (レガシー)** - カスタムJWT
- ✅ ユーザー名/パスワード認証
- ✅ 自前JWT発行
- ✅ 開発・テスト環境向け

## 機能

- ✅ WOFF OAuth 2.0認証（LINE WORKS）
- ✅ JWT認証（カスタム実装）
- ✅ gRPCインターセプターによる認証
- ✅ Buf CLIによるProtobuf管理
- ✅ BSR対応（フロントエンドとの統合準備完了）
- ✅ トークンキャッシュ・検証

## プロジェクト構成

```
jwt_mod/
├── proto/auth/v1/          # Protobuf定義
│   └── auth.proto
├── gen/                    # 生成されたコード
│   └── auth/v1/
├── cmd/
│   ├── server/            # gRPCサーバー
│   └── client/            # サンプルクライアント
├── internal/
│   ├── auth/              # JWT認証ロジック
│   └── interceptor/       # gRPCインターセプター
├── buf.yaml               # Buf設定
└── buf.gen.yaml           # コード生成設定
```

## 必要な環境

- Go 1.21以上
- Buf CLI

## セットアップ

### 1. 依存関係のインストール

```bash
go mod download
```

### 2. Protobufコードの生成

```bash
buf generate
```

## クイックスタート

### WOFF Login (推奨)

詳細は **[WOFF_README.md](WOFF_README.md)** を参照してください。

```bash
# 環境変数を設定
export WOFF_CLIENT_ID=your-client-id
export WOFF_CLIENT_SECRET=your-client-secret
export WOFF_REDIRECT_URI=http://localhost:8080/callback

# WOFFサーバーを起動
go run cmd/server/woff_main.go

# 別ターミナルでコールバックサーバーを起動
go run examples/callback-server/main.go

# ブラウザで http://localhost:8080 を開く
```

### JWT Authentication (レガシー)

```bash
# JWTサーバーを起動
go run cmd/server/main.go

# 別ターミナルでクライアントを実行
go run cmd/client/main.go
```

サーバーはポート50051で起動します。

### クライアントのテスト実行

別のターミナルで以下を実行：

```bash
go run cmd/client/main.go
```

### デモユーザー情報

- **ユーザー名**: `demo`
- **パスワード**: `password123`

## API エンドポイント

### 1. Login (認証不要)

ユーザーをログインしてJWTトークンを取得します。

```protobuf
rpc Login(LoginRequest) returns (LoginResponse);
```

**リクエスト例:**
```json
{
  "username": "demo",
  "password": "password123"
}
```

**レスポンス例:**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIs...",
  "expires_in": 900
}
```

### 2. GetProfile (認証必要)

認証されたユーザーのプロファイルを取得します。

```protobuf
rpc GetProfile(GetProfileRequest) returns (GetProfileResponse);
```

**ヘッダー:**
```
Authorization: Bearer <access_token>
```

**レスポンス例:**
```json
{
  "user_id": "1",
  "username": "demo",
  "email": "demo@example.com",
  "roles": ["user", "admin"]
}
```

### 3. RefreshToken (認証不要)

トークンをリフレッシュして新しいアクセストークンを取得します。

```protobuf
rpc RefreshToken(RefreshTokenRequest) returns (RefreshTokenResponse);
```

## Buf Schema Registry (BSR) との統合

### BSRへのプッシュ

1. **Bufにログイン:**

```bash
buf registry login
```

2. **リポジトリの作成:**

BSRウェブコンソールで新しいリポジトリを作成（例: `your-org/jwt-auth`）

3. **buf.yamlの更新:**

```yaml
version: v2
modules:
  - path: proto
    name: buf.build/your-org/jwt-auth  # あなたの組織名とリポジトリ名
lint:
  use:
    - STANDARD
breaking:
  use:
    - FILE
```

4. **プッシュ:**

```bash
buf push
```

### フロントエンドでの利用

フロントエンド（TypeScript/JavaScript）でBSRから生成されたコードを使用する例：

**1. パッケージのインストール:**

```bash
# Connect-Webの場合
npm install @bufbuild/protobuf @connectrpc/connect @connectrpc/connect-web

# BSRからスキーマをインストール
npm install @buf/your-org_jwt-auth.connectrpc_es@latest
```

**2. クライアントコード例 (TypeScript):**

```typescript
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "@buf/your-org_jwt-auth.connectrpc_es/auth/v1/auth_connect";

// トランスポートの作成
const transport = createConnectTransport({
  baseUrl: "http://localhost:8080",
});

// クライアントの作成
const client = createPromiseClient(AuthService, transport);

// ログイン
async function login() {
  const response = await client.login({
    username: "demo",
    password: "password123",
  });

  console.log("Token:", response.accessToken);
  return response.accessToken;
}

// プロファイル取得
async function getProfile(token: string) {
  const response = await client.getProfile(
    {},
    {
      headers: {
        Authorization: `Bearer ${token}`,
      },
    }
  );

  console.log("Profile:", response);
}
```

## grpcurlでのテスト

grpcurlを使用してAPIをテストすることもできます：

```bash
# ログイン
grpcurl -plaintext -d '{"username":"demo","password":"password123"}' \
  localhost:50051 auth.v1.AuthService/Login

# プロファイル取得（トークンを置き換えてください）
grpcurl -plaintext \
  -H "authorization: Bearer YOUR_TOKEN_HERE" \
  localhost:50051 auth.v1.AuthService/GetProfile
```

## セキュリティに関する注意

⚠️ **本番環境での注意点:**

1. **シークレットキーの管理**: 環境変数から読み込むようにしてください
```go
secretKey := os.Getenv("JWT_SECRET_KEY")
```

2. **HTTPS/TLSの使用**: 本番環境では必ずTLSを有効にしてください

3. **トークンの有効期限**: 適切な有効期限を設定してください

4. **パスワードハッシュ**: bcryptを使用していますが、ソルトラウンド数を調整してください

5. **レート制限**: ログインエンドポイントにレート制限を実装してください

## カスタマイズ

### トークンの有効期限を変更

[cmd/server/main.go:16](cmd/server/main.go#L16) で設定を変更：

```go
const (
    secretKey     = "your-secret-key"
    tokenDuration = 1 * time.Hour  // 1時間に変更
    port          = 50051
)
```

### 新しいユーザーの追加

[internal/auth/user.go:36](internal/auth/user.go#L36) でユーザーを追加：

```go
hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("newpassword"), bcrypt.DefaultCost)
store.users["newuser"] = &User{
    ID:           "2",
    Username:     "newuser",
    Email:        "newuser@example.com",
    PasswordHash: string(hashedPassword),
    Roles:        []string{"user"},
}
```

## トラブルシューティング

### コード生成エラー

```bash
buf mod update
buf generate
```

### 依存関係の問題

```bash
go mod tidy
go mod download
```

## ライセンス

MIT License

## 参考リンク

- [Buf Documentation](https://buf.build/docs)
- [gRPC Go](https://grpc.io/docs/languages/go/)
- [JWT Go Library](https://github.com/golang-jwt/jwt)
- [Connect-Web](https://connectrpc.com/docs/web/getting-started)
