# フロントエンド クイックフィックス

## 問題

❌ ブラウザコンソールに以下のエラーが表示される:
```
Failed to fetch profile: ConnectError: [unknown] rpc error: code = Unauthenticated desc = authentication required - user info is already included in ExchangeCode response
```

## 原因

フロントエンドが不要な`GetProfile` APIを呼んでいます。
**実際には認証は成功しています！**

## 解決方法

### 修正前（❌ 削除してください）

```javascript
const tokenResponse = await client.exchangeCode({ code, state, redirectUri });
const profile = await client.getProfile(); // ← 削除！
```

### 修正後（✅ これだけでOK）

```javascript
const response = await client.exchangeCode({ code, state, redirectUri });

// ユーザー情報は既にレスポンスに含まれています！
const user = {
  userId: response.userId,          // ✅ 既に含まれている
  userName: response.userName,      // ✅ 既に含まれている
  email: response.email,            // ✅ 既に含まれている
  displayName: response.displayName,// ✅ 既に含まれている
  accessToken: response.accessToken,// ✅ 既に含まれている
  refreshToken: response.refreshToken,// ✅ 既に含まれている
};
```

## 変更箇所

1. `getProfile()`の呼び出しを**削除**
2. `exchangeCode()`のレスポンスから直接ユーザー情報を取得

## これだけ！

たった1行削除するだけです:

```diff
  const response = await client.exchangeCode({ code, state, redirectUri });
- const profile = await client.getProfile();

  setUser({
-   id: profile.userId,
-   name: profile.userName,
+   id: response.userId,
+   name: response.userName,
    // ...
  });
```

## バックエンドの状態

✅ 認証は完全に成功しています
✅ ユーザー情報はデータベースに保存されています
✅ トークンは正常に発行されています

バックエンドログ:
```
ExchangeCode レスポンス返送: ユーザーID=f928d399-5b3a-4b83-19f0-04d18ea13ea6, 名前=本多 優鷹, メール=ohishi.yoshitaka.honda@ohishiunyusouko
```

## 詳細ドキュメント

詳しくは `FRONTEND_INTEGRATION.md` を参照してください。
