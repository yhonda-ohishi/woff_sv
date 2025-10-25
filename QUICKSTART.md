# クイックスタートガイド

5分でJWT gRPC サーバーを起動して試すためのガイドです。

## 前提条件

- Go 1.21以上がインストールされていること
- Buf CLIがインストールされていること（オプション）

## ステップ1: サーバーの起動

```bash
# サーバーを起動
go run cmd/server/main.go
```

または、ビルド済みのバイナリを使用：

```bash
# ビルド
go build -o bin/server cmd/server/main.go

# 実行
./bin/server
```

以下のようなメッセージが表示されます：

```
gRPC server listening on port 50051
Demo credentials - Username: demo, Password: password123
```

## ステップ2: クライアントでテスト

別のターミナルを開いて：

```bash
go run cmd/client/main.go
```

または：

```bash
./bin/client
```

以下のような出力が表示されます：

```
Connected to gRPC server at localhost:50051

=== Test 1: Login ===
Login successful!
Access token: eyJhbGciOiJIUzI1NiIs...
Token expires in: 900 seconds

=== Test 2: Get Profile (authenticated) ===
User Profile:
  User ID: 1
  Username: demo
  Email: demo@example.com
  Roles: [user admin]

=== Test 3: Get Profile (unauthenticated - should fail) ===
Expected error: rpc error: code = Unauthenticated desc = authorization token is not provided

=== Test 4: Login with invalid credentials (should fail) ===
Expected error: rpc error: code = Unauthenticated desc = invalid credentials

=== All tests completed successfully! ===
```

## grpcurlを使用したテスト（オプション）

grpcurlをインストールしている場合：

### サービスの確認

```bash
grpcurl -plaintext localhost:50051 list
```

出力:
```
auth.v1.AuthService
grpc.reflection.v1alpha.ServerReflection
```

### ログイン

```bash
grpcurl -plaintext -d '{"username":"demo","password":"password123"}' \
  localhost:50051 auth.v1.AuthService/Login
```

出力例:
```json
{
  "accessToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refreshToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expiresIn": "900"
}
```

### プロファイル取得

```bash
# TOKENを上記のaccessTokenに置き換えてください
TOKEN="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."

grpcurl -plaintext \
  -H "authorization: Bearer $TOKEN" \
  localhost:50051 auth.v1.AuthService/GetProfile
```

出力例:
```json
{
  "userId": "1",
  "username": "demo",
  "email": "demo@example.com",
  "roles": ["user", "admin"]
}
```

## Makefileを使用（Linuxのみ）

Makefileを使用すると、コマンドを簡単に実行できます：

```bash
# ヘルプを表示
make help

# コード生成
make generate

# ビルド
make build

# サーバー起動
make run-server

# クライアント実行（別ターミナル）
make run-client
```

## 次のステップ

1. **カスタマイズ**:
   - [cmd/server/main.go](cmd/server/main.go) でサーバー設定を変更
   - [internal/auth/user.go](internal/auth/user.go) で新しいユーザーを追加

2. **BSRへのプッシュ**:
   - [README.md](README.md) のBSRセクションを参照

3. **フロントエンド統合**:
   - [examples/frontend-integration.md](examples/frontend-integration.md) を参照

4. **本番環境への展開**:
   - シークレットキーを環境変数に移動
   - TLS/HTTPSを有効化
   - データベースを統合
   - レート制限を実装

## トラブルシューティング

### ポートが使用中

```bash
# Windowsの場合
netstat -ano | findstr :50051

# Linux/macの場合
lsof -i :50051
```

ポートを変更する場合は、[cmd/server/main.go:15](cmd/server/main.go#L15) を編集してください。

### 依存関係エラー

```bash
go mod tidy
go mod download
```

### コード生成エラー

```bash
# Buf CLIをインストール
go install github.com/bufbuild/buf/cmd/buf@latest

# コード生成
buf generate
```

## サポート

問題が発生した場合：
1. [README.md](README.md) の詳細なドキュメントを確認
2. GitHubリポジトリにIssueを作成
3. [Buf documentation](https://buf.build/docs) を参照

## デモユーザー

デフォルトで以下のユーザーが利用可能です：

| ユーザー名 | パスワード | ロール |
|-----------|----------|--------|
| demo      | password123 | user, admin |

新しいユーザーを追加するには、[internal/auth/user.go](internal/auth/user.go) を編集してください。
