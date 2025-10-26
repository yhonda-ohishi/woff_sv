#!/bin/bash

# Test recording upload endpoint

echo "Testing /api/recordings/upload endpoint..."

curl -X POST http://localhost:50051/api/recordings/upload \
  -F "video=@test_recordings/test_recording.webm" \
  -F "sessionId=test-session-123" \
  -F "userId=test-user-456" \
  -F "roomId=test-room" \
  -F "timestamp=$(date +%s)000" \
  -F "partNumber=0" \
  -v

echo ""
echo "Test complete!"
