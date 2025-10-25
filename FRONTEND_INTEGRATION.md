# フロントエンド統合ガイド

## 概要

WOFF認証バックエンドは完全に動作しています。このドキュメントでは、フロントエンドでの統合方法を説明します。

## 現在の問題

フロントエンドが以下の古いパターンを使用しています:

```javascript
// ❌ 古いパターン（現在のフロントエンド）
const tokenResponse = await client.exchangeCode({ code, state, redirectUri });
const profileResponse = await client.getProfile(); // ← これは不要！
```

## 推奨される実装

`ExchangeCode`のレスポンスには**既に全てのユーザー情報が含まれています**。`GetProfile`を呼ぶ必要はありません。

### ✅ 新しいパターン（推奨）

```javascript
// ✅ 正しいパターン
const response = await client.exchangeCode({ code, state, redirectUri });

// ExchangeCodeレスポンスに全ての情報が含まれています
const user = {
  userId: response.userId,
  userName: response.userName,
  email: response.email,
  displayName: response.displayName,
  domainId: response.domainId,
  roles: response.roles,
  profileImageUrl: response.profileImageUrl,
  accessToken: response.accessToken,
  refreshToken: response.refreshToken,
  expiresIn: response.expiresIn,
};

// GetProfileは不要！
```

## ExchangeCodeレスポンスの構造

```typescript
interface ExchangeCodeResponse {
  // トークン情報
  access_token: string;
  refresh_token: string;
  expires_in: number;
  token_type: string;
  scope: string[];

  // ユーザー情報（追加済み）
  user_id: string;           // 例: "f928d399-5b3a-4b83-19f0-04d18ea13ea6"
  user_name: string;          // 例: "本多 優鷹"
  email: string;              // 例: "ohishi.yoshitaka.honda@ohishiunyusouko"
  display_name: string;       // 例: "本多"
  domain_id: string;          // 例: "10133534"
  roles: string[];            // 例: []
  profile_image_url: string;  // プロフィール画像URL
}
```

## 修正が必要な箇所

### 1. 認証コールバック処理

**修正前:**
```javascript
async function handleAuthCallback(code: string, state: string) {
  try {
    // トークン交換
    const tokenResponse = await authClient.exchangeCode({
      code,
      state,
      redirectUri: CALLBACK_URL,
    });

    // ❌ 不要な GetProfile 呼び出し
    const profile = await authClient.getProfile();

    // ユーザー情報を保存
    setUser({
      id: profile.userId,
      name: profile.userName,
      email: profile.email,
    });

    // トークンを保存
    saveTokens(tokenResponse.accessToken, tokenResponse.refreshToken);
  } catch (error) {
    console.error('認証エラー:', error);
  }
}
```

**修正後:**
```javascript
async function handleAuthCallback(code: string, state: string) {
  try {
    // トークン交換（ユーザー情報も含む）
    const response = await authClient.exchangeCode({
      code,
      state,
      redirectUri: CALLBACK_URL,
    });

    // ✅ ExchangeCodeレスポンスから直接ユーザー情報を取得
    setUser({
      id: response.userId,
      name: response.userName,
      email: response.email,
      displayName: response.displayName,
      domainId: response.domainId,
      roles: response.roles,
      profileImageUrl: response.profileImageUrl,
    });

    // トークンを保存
    saveTokens(response.accessToken, response.refreshToken);

    // 成功！
    console.log('認証成功:', response.userName);
  } catch (error) {
    console.error('認証エラー:', error);
  }
}
```

### 2. エラーハンドリングの改善

現在、フロントエンドは`GetProfile`のエラーを表示していますが、これは無視できます。

```javascript
// ❌ 削除してください
try {
  const profile = await authClient.getProfile();
} catch (error) {
  // このエラーは表示されますが、実際には認証は成功しています
  console.error('Failed to fetch profile:', error);
}
```

## バックエンドの動作確認

バックエンドログで以下のメッセージが確認できれば、認証は成功しています:

```
✅ 処理完了を検知、キャッシュされたレスポンスを返します: code=jp1...
ExchangeCode レスポンス返送: ユーザーID=f928d399-5b3a-4b83-19f0-04d18ea13ea6, 名前=本多 優鷹, メール=ohishi.yoshitaka.honda@ohishiunyusouko
```

以下のエラーは無視してください（フロントエンドが不要な`GetProfile`を呼んでいるため）:

```
⚠️ GetProfile呼び出し - 認証コンテキストなし。ExchangeCodeレスポンスに既にユーザー情報が含まれているため、このAPIは不要です
```

## テスト方法

1. ログインボタンをクリック
2. LINE WORKS認証画面で認証
3. コールバックURLにリダイレクト
4. `ExchangeCode`が呼ばれる
5. レスポンスにユーザー情報が含まれていることを確認

### デバッグ用コンソールログ

```javascript
const response = await authClient.exchangeCode({ code, state, redirectUri });
console.log('認証成功:', {
  userId: response.userId,
  userName: response.userName,
  email: response.email,
  accessToken: response.accessToken ? '✓' : '✗',
  refreshToken: response.refreshToken ? '✓' : '✗',
});
```

## バックエンド側の対応状況

✅ **完了済み:**
- ExchangeCodeレスポンスにユーザー情報を追加
- 重複リクエスト対策（ブラウザの自動リトライに対応）
- トークン交換の成功
- データベースへのユーザー保存

⚠️ **非推奨（後方互換性のみ）:**
- GetProfile エンドポイント（認証コンテキストが必要）

## 現在のバックエンドURL

バックエンドは起動時に自動的にフロントエンドに登録されます:

```
Backend URL: https://[random].trycloudflare.com
```

フロントエンドのプロキシ設定で以下のパスが使用されます:
```
https://ohishi-pwa-woff.mtamaramu.com/api/auth.v1.AuthService/*
```

## まとめ

### 必要な変更

1. ✅ `GetProfile`呼び出しを削除
2. ✅ `ExchangeCode`レスポンスからユーザー情報を直接取得
3. ✅ エラーハンドリングを簡素化

### メリット

- 🚀 APIコール数が減る（1回のリクエストで完結）
- ✅ エラーが減る（GetProfileエラーがなくなる）
- ⚡ パフォーマンス向上
- 🔒 セキュリティ向上（トークンを複数回送信しない）

## サンプルコード（完全版）

```typescript
import { createPromiseClient } from "@connectrpc/connect";
import { AuthService } from "./gen/auth/v1/auth_connect";

// クライアントの作成
const client = createPromiseClient(AuthService, transport);

// 認証フロー
async function login() {
  // 1. 認証URLを取得
  const { authorizationUrl, state } = await client.getAuthorizationURL({
    redirectUri: "https://ohishi-pwa-woff.mtamaramu.com/callback",
    scopes: ["user", "user.read"], // バックエンドが自動的に正しいスコープに変換
  });

  // 2. 認証URLにリダイレクト
  window.location.href = authorizationUrl;
}

// コールバック処理
async function handleCallback(code: string, state: string) {
  try {
    // 3. コードをトークンと交換（ユーザー情報も取得）
    const response = await client.exchangeCode({
      code,
      state,
      redirectUri: "https://ohishi-pwa-woff.mtamaramu.com/callback",
    });

    // 4. ユーザー情報とトークンを保存
    localStorage.setItem("accessToken", response.accessToken);
    localStorage.setItem("refreshToken", response.refreshToken);
    localStorage.setItem("user", JSON.stringify({
      id: response.userId,
      name: response.userName,
      email: response.email,
      displayName: response.displayName,
      roles: response.roles,
    }));

    // 5. 成功！ホーム画面にリダイレクト
    window.location.href = "/";

  } catch (error) {
    console.error("認証失敗:", error);
    // エラー画面を表示
  }
}
```

## サポート

問題が発生した場合は、バックエンドのログを確認してください。認証が成功しているかどうかは、以下のログメッセージで判断できます:

```
ExchangeCode レスポンス返送: ユーザーID=..., 名前=..., メール=...
```

このメッセージが表示されていれば、バックエンド側は正常に動作しています。
