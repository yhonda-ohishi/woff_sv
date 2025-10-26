#!/bin/bash
echo "Testing separate audio/video upload..."
curl -X POST http://localhost:50051/api/recordings/upload \
  -F "video=@test_recordings/test_video_only.webm" \
  -F "audio=@test_recordings/test_audio_only.webm" \
  -F "sessionId=test-session-separate" \
  -F "userId=test-user-456" \
  -F "roomId=test-room" \
  -F "timestamp=$(date +%s)000" \
  -F "partNumber=0" \
  -v
