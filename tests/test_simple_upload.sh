#!/bin/bash
curl -X POST http://localhost:50051/api/test-flickr-upload \
  -F "file=@test.jpg" \
  -F "title=Test Image" \
  -F "description=Simple test upload"
