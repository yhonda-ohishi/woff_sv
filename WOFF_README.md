# WOFF (LINE WORKS) Login gRPC Server

GoでLINE WORKS OAuth 2.0 (WOFF Login)を実装したgRPCサーバーのプロジェクトです。Buf Schema Registry (BSR)を使用してフロントエンドとの統合を簡単にします。

## 🎯 機能

- ✅ WOFF OAuth 2.0認証フロー
- ✅ 認証URLの生成
- ✅ 認証コードとトークンの交換
- ✅ ユーザー情報取得
- ✅ トークンリフレッシュ
- ✅ トークン検証
- ✅ gRPCインターセプターによる認証
- ✅ Buf Schema Registry対応

## 📋 前提条件

### LINE WORKS Developer Console設定

1. [LINE WORKS Developer Console](https://developers.worksmobile.com/)にログイン
2. 新しいアプリケーションを作成
3. OAuth 2.0設定:
   - **Redirect URI**: `http://localhost:8080/callback` (開発環境)
   - **Scopes**: `user`, `user.read`
4. **Client ID**と**Client Secret**を取得

### 環境要件

- Go 1.21以上
- Buf CLI
- LINE WORKSアカウント

## 🚀 クイックスタート

### 1. 環境変数の設定

`.env`ファイルを作成（`.env.example`をコピー）:

```bash
cp .env.example .env
```

`.env`ファイルを編集:

```bash
# WOFF (LINE WORKS) OAuth Configuration
WOFF_CLIENT_ID=your-actual-client-id
WOFF_CLIENT_SECRET=your-actual-client-secret
WOFF_REDIRECT_URI=http://localhost:8080/callback
```

### 2. 依存関係のインストール

```bash
go mod download
```

### 3. サーバーの起動

```bash
# 環境変数を読み込んで起動
export $(cat .env | xargs)
go run cmd/server/woff_main.go
```

または、直接環境変数を指定:

```bash
WOFF_CLIENT_ID=xxx WOFF_CLIENT_SECRET=yyy go run cmd/server/woff_main.go
```

### 4. クライアントでテスト

別のターミナルで:

```bash
go run cmd/client/woff_main.go
```

## 📖 認証フロー

### OAuth 2.0 Authorization Code Flow

```
1. クライアント → GetAuthorizationURL → サーバー
   ↓
2. サーバー → 認証URL生成 → クライアント
   ↓
3. ユーザー → ブラウザで認証URL → LINE WORKS
   ↓
4. LINE WORKS → 認証後リダイレクト → コールバックURL (code付き)
   ↓
5. クライアント → ExchangeCode → サーバー
   ↓
6. サーバー → LINE WORKS API → アクセストークン取得
   ↓
7. クライアント → GetProfile (token付き) → サーバー
   ↓
8. サーバー → ユーザー情報返却 → クライアント
```

## 🔌 API エンドポイント

### 1. GetAuthorizationURL (認証不要)

WOFF OAuth認証URLを取得します。

```protobuf
rpc GetAuthorizationURL(GetAuthorizationURLRequest) returns (GetAuthorizationURLResponse);
```

**リクエスト:**
```json
{
  "redirect_uri": "http://localhost:8080/callback",
  "state": "random-state-string",
  "scopes": ["user", "user.read"]
}
```

**レスポンス:**
```json
{
  "authorization_url": "https://auth.worksmobile.com/oauth2/v2.0/authorize?...",
  "state": "random-state-string"
}
```

### 2. ExchangeCode (認証不要)

認証コードをアクセストークンと交換します。

```protobuf
rpc ExchangeCode(ExchangeCodeRequest) returns (ExchangeCodeResponse);
```

**リクエスト:**
```json
{
  "code": "authorization-code-from-callback",
  "redirect_uri": "http://localhost:8080/callback",
  "state": "random-state-string"
}
```

**レスポンス:**
```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_in": 3600,
  "token_type": "Bearer",
  "scope": ["user", "user.read"]
}
```

### 3. GetProfile (認証必要)

ユーザープロファイルを取得します。

```protobuf
rpc GetProfile(GetProfileRequest) returns (GetProfileResponse);
```

**ヘッダー:**
```
Authorization: Bearer <access_token>
```

**レスポンス:**
```json
{
  "user_id": "user123",
  "user_name": "john.doe",
  "email": "john.doe@company.com",
  "display_name": "John Doe",
  "domain_id": "company",
  "roles": ["user", "admin"],
  "profile_image_url": "https://..."
}
```

### 4. RefreshToken (認証不要)

アクセストークンをリフレッシュします。

```protobuf
rpc RefreshToken(RefreshTokenRequest) returns (RefreshTokenResponse);
```

### 5. VerifyToken (認証不要)

トークンの有効性を検証します。

```protobuf
rpc VerifyToken(VerifyTokenRequest) returns (VerifyTokenResponse);
```

## 💻 実装例

### Goクライアント

```go
// 1. 認証URL取得
authURL, state, err := client.GetAuthorizationURL(ctx, &authv1.GetAuthorizationURLRequest{
    RedirectUri: "http://localhost:8080/callback",
    Scopes:      []string{"user", "user.read"},
})

// 2. ユーザーをブラウザで認証URLに誘導
// (ユーザーが認証後、codeパラメータ付きでリダイレクトされる)

// 3. コードをトークンに交換
tokenResp, err := client.ExchangeCode(ctx, &authv1.ExchangeCodeRequest{
    Code:        authCode,
    RedirectUri: "http://localhost:8080/callback",
    State:       state,
})

// 4. トークンを使ってプロファイル取得
md := metadata.New(map[string]string{
    "authorization": "Bearer " + tokenResp.AccessToken,
})
ctx = metadata.NewOutgoingContext(ctx, md)

profile, err := client.GetProfile(ctx, &authv1.GetProfileRequest{})
```

### TypeScript/React (Connect-Web)

```typescript
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "@buf/your-org_woff-auth.connectrpc_es/auth/v1/auth_connect";

const transport = createConnectTransport({
  baseUrl: "http://localhost:8080",
});

const client = createPromiseClient(AuthService, transport);

// 1. 認証URL取得
const authUrlResponse = await client.getAuthorizationURL({
  redirectUri: "http://localhost:8080/callback",
  scopes: ["user", "user.read"],
});

// 2. ユーザーを認証URLへリダイレクト
window.location.href = authUrlResponse.authorizationUrl;

// 3. コールバックでコードを受け取り、トークンと交換
const urlParams = new URLSearchParams(window.location.search);
const code = urlParams.get('code');
const state = urlParams.get('state');

const tokenResponse = await client.exchangeCode({
  code: code,
  redirectUri: "http://localhost:8080/callback",
  state: state,
});

// トークンを保存
localStorage.setItem('access_token', tokenResponse.accessToken);

// 4. プロファイル取得
const profile = await client.getProfile(
  {},
  {
    headers: {
      Authorization: `Bearer ${tokenResponse.accessToken}`,
    },
  }
);
```

## 🔧 カスタマイズ

### スコープの変更

[cmd/server/woff_main.go:138](cmd/server/woff_main.go#L138):

```go
woffConfig := &auth.WOFFConfig{
    ClientID:     clientID,
    ClientSecret: clientSecret,
    RedirectURI:  redirectURI,
    Scopes:       []string{"user", "user.read", "bot"}, // 追加スコープ
}
```

### リダイレクトURIの変更

環境変数で設定:

```bash
export WOFF_REDIRECT_URI=https://your-domain.com/auth/callback
```

## 🌐 本番環境デプロイ

### 1. HTTPS必須

本番環境では必ずHTTPSを使用してください:

```bash
WOFF_REDIRECT_URI=https://api.yourdomain.com/auth/callback
```

### 2. 環境変数の管理

Kubernetes Secretsや環境変数管理サービスを使用:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: woff-credentials
type: Opaque
stringData:
  client-id: "your-client-id"
  client-secret: "your-client-secret"
```

### 3. LINE WORKS Developer Console設定

本番環境のリダイレクトURIを登録:

```
https://api.yourdomain.com/auth/callback
```

## 🔐 セキュリティベストプラクティス

1. **State パラメータ**: CSRF攻撃防止のため必ず使用
2. **HTTPS**: 本番環境では必須
3. **Client Secret**: 環境変数で管理、コードにハードコードしない
4. **トークン保存**: 安全な場所に保存（HTTPOnly Cookie推奨）
5. **トークン有効期限**: 定期的にリフレッシュ
6. **スコープ最小化**: 必要最小限のスコープのみリクエスト

## 📂 プロジェクト構成

```
jwt_mod/
├── proto/auth/v1/
│   └── auth.proto              # WOFF対応API定義
├── internal/
│   ├── auth/
│   │   ├── woff.go            # WOFF OAuth実装
│   │   ├── jwt.go             # JWT実装（レガシー）
│   │   └── user.go            # ユーザー管理
│   └── interceptor/
│       ├── woff.go            # WOFFインターセプター
│       └── auth.go            # JWTインターセプター（レガシー）
├── cmd/
│   ├── server/
│   │   ├── woff_main.go       # WOFFサーバー
│   │   └── main.go            # JWTサーバー（レガシー）
│   └── client/
│       ├── woff_main.go       # WOFFクライアント
│       └── main.go            # JWTクライアント（レガシー）
└── .env.example               # 環境変数サンプル
```

## 🐛 トラブルシューティング

### 「Invalid redirect_uri」エラー

LINE WORKS Developer Consoleで設定したリダイレクトURIと完全一致していることを確認:

```bash
# 設定値を確認
echo $WOFF_REDIRECT_URI
```

### 「Invalid client credentials」エラー

Client IDとClient Secretが正しいことを確認:

```bash
# 環境変数を確認
echo $WOFF_CLIENT_ID
echo $WOFF_CLIENT_SECRET
```

### トークン検証エラー

- トークンの有効期限を確認
- スコープが正しいか確認
- LINE WORKS APIエンドポイントが正しいか確認

### コールバックが受信できない

開発環境でコールバックサーバーを実装:

```go
http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
    code := r.URL.Query().Get("code")
    state := r.URL.Query().Get("state")
    // コードをトークンに交換
})
http.ListenAndServe(":8080", nil)
```

## 📚 参考リンク

- [LINE WORKS Developers](https://developers.worksmobile.com/)
- [WOFF API Documentation](https://developers.worksmobile.com/jp/docs/woff-api)
- [OAuth 2.0 Specification](https://oauth.net/2/)
- [Buf Schema Registry](https://buf.build/docs)
- [gRPC Go Documentation](https://grpc.io/docs/languages/go/)

## 🆚 JWT実装との違い

| 項目 | WOFF Login | JWT (レガシー) |
|------|------------|----------------|
| 認証方式 | OAuth 2.0 | Username/Password |
| トークン発行元 | LINE WORKS | 自前サーバー |
| ユーザー管理 | LINE WORKS | 自前データベース |
| セキュリティ | OAuth標準 | カスタム実装 |
| シングルサインオン | 対応 | 非対応 |
| 推奨用途 | 本番環境 | 開発/テスト |

## 🎓 次のステップ

1. **コールバックサーバー実装**: HTTPサーバーでOAuthコールバックを処理
2. **フロントエンド統合**: React/Vue.jsでWOFF Loginを実装
3. **BSRプッシュ**: `buf push`でスキーマを共有
4. **本番環境設定**: HTTPS、環境変数、セキュリティ設定

## ライセンス

MIT License
