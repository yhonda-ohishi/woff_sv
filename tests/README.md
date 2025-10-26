# テストファイル

## アップロードテストスクリプト

### test_upload.sh
WebM録画ファイル（音声+動画）のアップロードテスト

```bash
bash tests/test_upload.sh
```

### test_simple_upload.sh
シンプルな画像ファイルのアップロードテスト

```bash
bash tests/test_simple_upload.sh
```

### test_separate_upload.sh
別々の音声・動画ファイルの合成とアップロードテスト

```bash
bash tests/test_separate_upload.sh
```

## テストファイル

### test_recordings/
テスト用のWebMファイルが含まれています
- `test_recording.webm` - 音声+動画のWebMファイル
- `test_video_only.webm` - 動画のみのWebMファイル
- `test_audio_only.webm` - 音声のみのWebMファイル

## 認証テスト

### test_flickr_auth.go
Flickr OAuth認証のテストプログラム（参考用）
