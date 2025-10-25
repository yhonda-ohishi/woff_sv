# フロントエンド統合ガイド

このドキュメントでは、React/TypeScript環境でBSRを使用してgRPCサーバーと統合する方法を説明します。

## 前提条件

1. Buf Schema Registryにprotoファイルがプッシュされていること
2. Node.js 18以上がインストールされていること

## セットアップ手順

### 1. プロジェクトの初期化

```bash
# React + TypeScriptプロジェクトを作成
npx create-react-app my-app --template typescript
cd my-app
```

### 2. 必要なパッケージのインストール

```bash
# Connect-Webとその依存関係
npm install @bufbuild/protobuf @connectrpc/connect @connectrpc/connect-web

# BSRから生成されたコードをインストール（組織名とリポジトリ名を置き換えてください）
npm install @buf/your-org_jwt-auth.connectrpc_es@latest \
            @buf/your-org_jwt-auth.bufbuild_es@latest
```

### 3. gRPC-Web Proxyのセットアップ

gRPCサーバーはHTTP/2を使用するため、ブラウザから直接接続できません。
Connect-Webを使用する場合、または grpc-webproxy を使用する方法があります。

#### オプション A: Connect-Webサーバー（推奨）

Goサーバーに Connect プロトコルのサポートを追加：

```go
// cmd/server/main.go に追加

import (
    "net/http"
    "golang.org/x/net/http2"
    "golang.org/x/net/http2/h2c"
)

// HTTPハンドラーの追加
func main() {
    // ... existing gRPC server setup ...

    // Connect-Web用のHTTPサーバーを追加
    mux := http.NewServeMux()

    // gRPCサーバーをHTTPハンドラーとしてマウント
    mux.Handle("/", h2c.NewHandler(grpcServer, &http2.Server{}))

    // CORS設定
    handler := cors.New(cors.Options{
        AllowedOrigins:   []string{"http://localhost:3000"},
        AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowedHeaders:   []string{"*"},
        AllowCredentials: true,
    }).Handler(mux)

    // HTTPサーバーを起動
    httpServer := &http.Server{
        Addr:    ":8080",
        Handler: handler,
    }

    go func() {
        log.Printf("HTTP server listening on port 8080 for Connect-Web")
        if err := httpServer.ListenAndServe(); err != nil {
            log.Fatalf("Failed to serve HTTP: %v", err)
        }
    }()

    // ... existing gRPC server start ...
}
```

#### オプション B: Envoy Proxy

`envoy.yaml` を作成：

```yaml
static_resources:
  listeners:
    - name: listener_0
      address:
        socket_address:
          address: 0.0.0.0
          port_value: 8080
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                codec_type: auto
                stat_prefix: ingress_http
                route_config:
                  name: local_route
                  virtual_hosts:
                    - name: local_service
                      domains: ["*"]
                      routes:
                        - match:
                            prefix: "/"
                          route:
                            cluster: grpc_service
                      cors:
                        allow_origin_string_match:
                          - prefix: "*"
                        allow_methods: GET, PUT, DELETE, POST, OPTIONS
                        allow_headers: keep-alive,user-agent,cache-control,content-type,content-transfer-encoding,custom-header-1,x-accept-content-transfer-encoding,x-accept-response-streaming,x-user-agent,x-grpc-web,grpc-timeout,authorization
                        max_age: "1728000"
                        expose_headers: custom-header-1,grpc-status,grpc-message
                http_filters:
                  - name: envoy.filters.http.grpc_web
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.grpc_web.v3.GrpcWeb
                  - name: envoy.filters.http.cors
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.cors.v3.Cors
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  clusters:
    - name: grpc_service
      connect_timeout: 0.25s
      type: logical_dns
      http2_protocol_options: {}
      lb_policy: round_robin
      load_assignment:
        cluster_name: grpc_service
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address:
                      address: host.docker.internal
                      port_value: 50051
```

Docker Composeで起動：

```yaml
version: '3'
services:
  envoy:
    image: envoyproxy/envoy:v1.28-latest
    ports:
      - "8080:8080"
    volumes:
      - ./envoy.yaml:/etc/envoy/envoy.yaml
    command: /usr/local/bin/envoy -c /etc/envoy/envoy.yaml
```

### 4. Reactクライアントの実装

#### 認証サービスの作成

`src/services/authService.ts`:

```typescript
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "@buf/your-org_jwt-auth.connectrpc_es/auth/v1/auth_connect";

// トランスポートの作成
const transport = createConnectTransport({
  baseUrl: "http://localhost:8080",
});

// クライアントの作成
export const authClient = createPromiseClient(AuthService, transport);

// トークン管理
export const tokenManager = {
  getAccessToken: () => localStorage.getItem("access_token"),
  getRefreshToken: () => localStorage.getItem("refresh_token"),
  setTokens: (accessToken: string, refreshToken: string) => {
    localStorage.setItem("access_token", accessToken);
    localStorage.setItem("refresh_token", refreshToken);
  },
  clearTokens: () => {
    localStorage.removeItem("access_token");
    localStorage.removeItem("refresh_token");
  },
};

// ログイン
export async function login(username: string, password: string) {
  try {
    const response = await authClient.login({
      username,
      password,
    });

    tokenManager.setTokens(response.accessToken, response.refreshToken);
    return response;
  } catch (error) {
    console.error("Login failed:", error);
    throw error;
  }
}

// プロファイル取得
export async function getProfile() {
  const token = tokenManager.getAccessToken();
  if (!token) {
    throw new Error("No access token found");
  }

  try {
    const response = await authClient.getProfile(
      {},
      {
        headers: {
          Authorization: `Bearer ${token}`,
        },
      }
    );
    return response;
  } catch (error) {
    console.error("Get profile failed:", error);
    throw error;
  }
}

// トークンリフレッシュ
export async function refreshToken() {
  const refreshTokenValue = tokenManager.getRefreshToken();
  if (!refreshTokenValue) {
    throw new Error("No refresh token found");
  }

  try {
    const response = await authClient.refreshToken({
      refreshToken: refreshTokenValue,
    });

    // 新しいアクセストークンを保存
    const currentRefreshToken = tokenManager.getRefreshToken()!;
    tokenManager.setTokens(response.accessToken, currentRefreshToken);
    return response;
  } catch (error) {
    console.error("Token refresh failed:", error);
    tokenManager.clearTokens();
    throw error;
  }
}
```

#### Reactコンポーネントの例

`src/components/LoginForm.tsx`:

```typescript
import React, { useState } from "react";
import { login } from "../services/authService";

export function LoginForm() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");

    try {
      await login(username, password);
      window.location.href = "/dashboard";
    } catch (err) {
      setError("ログインに失敗しました");
    }
  };

  return (
    <form onSubmit={handleSubmit}>
      <h2>ログイン</h2>
      {error && <div className="error">{error}</div>}
      <div>
        <label>
          ユーザー名:
          <input
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </label>
      </div>
      <div>
        <label>
          パスワード:
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </label>
      </div>
      <button type="submit">ログイン</button>
    </form>
  );
}
```

`src/components/Profile.tsx`:

```typescript
import React, { useEffect, useState } from "react";
import { getProfile } from "../services/authService";
import { GetProfileResponse } from "@buf/your-org_jwt-auth.bufbuild_es/auth/v1/auth_pb";

export function Profile() {
  const [profile, setProfile] = useState<GetProfileResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    async function loadProfile() {
      try {
        const data = await getProfile();
        setProfile(data);
      } catch (err) {
        setError("プロファイルの取得に失敗しました");
      } finally {
        setLoading(false);
      }
    }

    loadProfile();
  }, []);

  if (loading) return <div>読み込み中...</div>;
  if (error) return <div className="error">{error}</div>;
  if (!profile) return null;

  return (
    <div className="profile">
      <h2>プロファイル</h2>
      <p><strong>ユーザーID:</strong> {profile.userId}</p>
      <p><strong>ユーザー名:</strong> {profile.username}</p>
      <p><strong>メール:</strong> {profile.email}</p>
      <p><strong>ロール:</strong> {profile.roles.join(", ")}</p>
    </div>
  );
}
```

#### 認証コンテキストの作成

`src/contexts/AuthContext.tsx`:

```typescript
import React, { createContext, useContext, useState, useEffect } from "react";
import { login as authLogin, getProfile, tokenManager } from "../services/authService";

interface AuthContextType {
  isAuthenticated: boolean;
  user: any | null;
  login: (username: string, password: string) => Promise<void>;
  logout: () => void;
  loading: boolean;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [isAuthenticated, setIsAuthenticated] = useState(false);
  const [user, setUser] = useState(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    // 初期化時にトークンをチェック
    async function checkAuth() {
      const token = tokenManager.getAccessToken();
      if (token) {
        try {
          const profile = await getProfile();
          setUser(profile);
          setIsAuthenticated(true);
        } catch (error) {
          tokenManager.clearTokens();
        }
      }
      setLoading(false);
    }

    checkAuth();
  }, []);

  const login = async (username: string, password: string) => {
    await authLogin(username, password);
    const profile = await getProfile();
    setUser(profile);
    setIsAuthenticated(true);
  };

  const logout = () => {
    tokenManager.clearTokens();
    setUser(null);
    setIsAuthenticated(false);
  };

  return (
    <AuthContext.Provider value={{ isAuthenticated, user, login, logout, loading }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return context;
}
```

#### ルート保護

`src/components/PrivateRoute.tsx`:

```typescript
import React from "react";
import { Navigate } from "react-router-dom";
import { useAuth } from "../contexts/AuthContext";

interface PrivateRouteProps {
  children: React.ReactNode;
}

export function PrivateRoute({ children }: PrivateRouteProps) {
  const { isAuthenticated, loading } = useAuth();

  if (loading) {
    return <div>読み込み中...</div>;
  }

  return isAuthenticated ? <>{children}</> : <Navigate to="/login" />;
}
```

### 5. 自動トークンリフレッシュの実装

`src/services/authService.ts` に追加：

```typescript
import { Interceptor } from "@connectrpc/connect";

// トークンリフレッシュインターセプター
export const authInterceptor: Interceptor = (next) => async (req) => {
  const token = tokenManager.getAccessToken();
  if (token) {
    req.header.set("Authorization", `Bearer ${token}`);
  }

  try {
    return await next(req);
  } catch (error: any) {
    // トークンが期限切れの場合、リフレッシュを試みる
    if (error.code === "unauthenticated" && tokenManager.getRefreshToken()) {
      try {
        await refreshToken();
        // 新しいトークンで再試行
        const newToken = tokenManager.getAccessToken();
        if (newToken) {
          req.header.set("Authorization", `Bearer ${newToken}`);
          return await next(req);
        }
      } catch (refreshError) {
        // リフレッシュ失敗 - ログアウト
        tokenManager.clearTokens();
        window.location.href = "/login";
      }
    }
    throw error;
  }
};

// トランスポートにインターセプターを追加
const transport = createConnectTransport({
  baseUrl: "http://localhost:8080",
  interceptors: [authInterceptor],
});
```

## まとめ

これで、React/TypeScriptアプリケーションからBSRを通じてgRPCサーバーと通信できるようになりました。

主な利点：
- 型安全なAPI通信
- BSRによる自動コード生成
- Connect-Webによるブラウザ互換性
- JWT認証の統合

## トラブルシューティング

### CORSエラー
- サーバー側でCORS設定を確認
- プロキシ設定を確認

### 型エラー
- BSRパッケージのバージョンを確認
- `npm install` で依存関係を再インストール

### 接続エラー
- サーバーが起動しているか確認
- ポート番号が正しいか確認
- プロキシが正しく設定されているか確認
