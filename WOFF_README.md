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

# Database Configuration
DATABASE_PATH=woff.db

# Cloudflare Tunnel (公開URLを自動生成)
ENABLE_CLOUDFLARED=true
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

### 推奨: フロントエンド主導の認証フロー

Cloudflare Tunnelは起動毎にURLが変わるため、フロントエンド側でWOFF認証を完結させる方式を推奨します。

```
1. フロントエンド → LINE WORKS OAuth認証画面
   ↓
2. ユーザー認証 → LINE WORKS
   ↓
3. LINE WORKS → アクセストークン発行 → フロントエンド
   ↓
4. フロントエンド → gRPC呼び出し (Authorizationヘッダー付き) → バックエンド
   ↓
5. バックエンド → トークン検証 → LINE WORKS API
   ↓
6. バックエンド → ユーザー情報をDBに保存/更新
   ↓
7. バックエンド → レスポンス返却 → フロントエンド
```

**利点:**
- バックエンドのURLが変わっても影響なし
- フロントエンド側で柔軟な認証フロー制御が可能
- バックエンドはステートレスなトークン検証のみ

### 従来: バックエンド主導の認証フロー（固定URLの場合）

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

**注意:** この方式は固定URLが必要なため、Cloudflare Tunnel使用時は非推奨

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

### フロントエンド統合（推奨）

#### React + TypeScript の例

```typescript
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "@buf/yhonda_woff-auth.connectrpc_es/auth/v1/auth_connect";

// 1. gRPCクライアント設定
const transport = createConnectTransport({
  baseUrl: "https://your-cloudflare-tunnel-url.trycloudflare.com",
});

const client = createPromiseClient(AuthService, transport);

// 2. フロントエンドでWOFF認証（LINE WORKS JavaScript SDKを使用）
// https://developers.worksmobile.com/jp/docs/auth-overview
const loginWithWOFF = async () => {
  // LINE WORKS OAuth認証フローを実装
  // 例: ポップアップまたはリダイレクトで認証
  const accessToken = await worksmobile.auth.getAccessToken({
    client_id: "YOUR_CLIENT_ID",
    redirect_uri: "http://localhost:3000/callback",
    scope: "user user.read",
  });

  // LocalStorageなどに保存
  localStorage.setItem("woff_access_token", accessToken);
  return accessToken;
};

// 3. gRPC呼び出し時にトークンを付与
const getProfile = async () => {
  const accessToken = localStorage.getItem("woff_access_token");

  const profile = await client.getProfile(
    {}, // 空のリクエスト
    {
      headers: {
        Authorization: `Bearer ${accessToken}`,
      },
    }
  );

  console.log("User Profile:", profile);
  return profile;
};

// 4. トークン検証
const verifyToken = async (token: string) => {
  const result = await client.verifyToken({ accessToken: token });
  return result.valid;
};
```

#### Vue.js の例

```typescript
// composables/useWOFFAuth.ts
import { ref } from 'vue';
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "@buf/yhonda_woff-auth.connectrpc_es/auth/v1/auth_connect";

export function useWOFFAuth() {
  const accessToken = ref<string | null>(localStorage.getItem("woff_access_token"));
  const user = ref(null);

  const transport = createConnectTransport({
    baseUrl: import.meta.env.VITE_GRPC_URL,
  });

  const client = createPromiseClient(AuthService, transport);

  const login = async () => {
    // LINE WORKS認証実装
    const token = await worksmobile.auth.getAccessToken({
      client_id: import.meta.env.VITE_WOFF_CLIENT_ID,
      redirect_uri: window.location.origin + "/callback",
    });

    accessToken.value = token;
    localStorage.setItem("woff_access_token", token);
    await fetchProfile();
  };

  const fetchProfile = async () => {
    if (!accessToken.value) return;

    user.value = await client.getProfile({}, {
      headers: { Authorization: `Bearer ${accessToken.value}` },
    });
  };

  const logout = () => {
    accessToken.value = null;
    user.value = null;
    localStorage.removeItem("woff_access_token");
  };

  return { accessToken, user, login, fetchProfile, logout };
}
```

### Goクライアント（従来方式）

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

**注意:** この方式はバックエンドが固定URLの場合のみ使用してください。

## 🌐 Cloudflare Tunnel（公開URL自動生成）

### 機能
サーバー起動時に自動的にCloudflare Tunnelを起動し、インターネットからアクセス可能な公開URLを生成します。

### 前提条件
Cloudflaredのインストールが必要です：

**Windows:**
```bash
winget install --id Cloudflare.cloudflared
```

**Mac:**
```bash
brew install cloudflared
```

**Linux:**
```bash
# Debian/Ubuntu
wget -q https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
sudo dpkg -i cloudflared-linux-amd64.deb
```

### 使い方

1. 環境変数で有効化：
```bash
ENABLE_CLOUDFLARED=true
```

2. サーバー起動時に自動的に公開URLが表示されます：
```
🚀 Starting Cloudflare Tunnel...
✅ Cloudflare Tunnel started successfully!
╔════════════════════════════════════════════════════════════╗
║  🌐 Public URL: https://abc-def-123.trycloudflare.com    ║
║  🔗 gRPC URL:   abc-def-123.trycloudflare.com             ║
╚════════════════════════════════════════════════════════════╝
```

3. この公開URLを使ってどこからでもgRPCサーバーにアクセス可能

### 注意事項
- 無料のCloudflare Tunnelは一時的なURLを提供（サーバー再起動で変わります）
- 本番環境では固定のCloudflare Tunnelを使用することを推奨

## 🔧 カスタマイズ

### スコープの変更

[cmd/server/woff_main.go](cmd/server/woff_main.go):

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
