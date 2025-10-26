-- Recordings table for storing video call recording metadata
-- To be created in the development database (db_service)

CREATE TABLE IF NOT EXISTS recordings (
  id VARCHAR(255) PRIMARY KEY COMMENT 'Recording ID format: {timestamp}-{userId}-part{partNumber}',
  session_id VARCHAR(255) NOT NULL COMMENT 'Session ID to group recordings from the same call',
  user_id VARCHAR(255) NOT NULL COMMENT 'User ID who created the recording',
  room_id VARCHAR(255) NOT NULL COMMENT 'Room ID where the call took place',
  part_number INT NOT NULL DEFAULT 0 COMMENT 'Part number for split recordings (0, 1, 2...)',
  file_url TEXT NOT NULL COMMENT 'URL to the video file (Flickr URL)',
  file_size BIGINT NOT NULL COMMENT 'File size in bytes',
  duration INT NOT NULL COMMENT 'Recording duration in seconds (max 600 for 10 minutes)',
  timestamp BIGINT NOT NULL COMMENT 'Unix timestamp when recording started',
  uploaded_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT 'When the recording was uploaded to server',
  flickr_photo_id VARCHAR(255) COMMENT 'Flickr photo ID for the uploaded video',
  INDEX idx_session_id (session_id),
  INDEX idx_user_id (user_id),
  INDEX idx_timestamp (timestamp),
  UNIQUE KEY unique_session_part (session_id, part_number) COMMENT 'Prevent duplicate uploads'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='Video call recordings with Flickr storage';
