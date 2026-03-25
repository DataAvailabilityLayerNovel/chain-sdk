#!/bin/bash

# Query blob from Celestia by height and namespace
# Usage: ./query_celestia_blob.sh [height] [namespace_base64]

HEIGHT=${1:-620070}
# NAMESPACE=${2:-"AAAAAAAAAAAAAAAAAAAAAAAAAHJvbGx1cAAAAAA="}
NAMESPACE=${2:-"AAAAAAAAAAAAAAAAAAAAAAAAAABkZWZTZW5zb3I="}

CELESTIA_RPC=${CELESTIA_BRIDGE_RPC:-"http://131.153.224.169:26758"}
AUTH_TOKEN=${CELESTIA_AUTH_TOKEN:-"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJBbGxvdyI6WyJwdWJsaWMiLCJyZWFkIiwid3JpdGUiLCJhZG1pbiJdLCJOb25jZSI6Ii9qSklXZFM2aVl2dlhyUDJRei9YYzZTcGZoWStYZ25KbUFKMjJQVWFvQ0k9IiwiRXhwaXJlc0F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoifQ.L9hao6nSwQaQ2j-k3I-ogGErJ1c9GSR4Cc7kq7L2XGA"}

echo "🔍 Querying Celestia blob..."
echo "   Height: $HEIGHT"
echo "   Namespace: $NAMESPACE"
echo "   RPC: $CELESTIA_RPC"
echo ""

# Query blob
RESPONSE=$(curl -s -X POST "$CELESTIA_RPC" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -d "{
    \"jsonrpc\": \"2.0\",
    \"id\": 1,
    \"method\": \"blob.GetAll\",
    \"params\": [
      $HEIGHT,
      [\"$NAMESPACE\"]
    ]
  }")

echo "📦 Response:"
echo "$RESPONSE" | jq .

# Extract and decode data
echo ""
echo "📝 Decoded data:"
DATA=$(echo "$RESPONSE" | jq -r '.result[0].data')
if [ "$DATA" != "null" ] && [ -n "$DATA" ]; then
  echo "$DATA" | base64 -d | jq .
else
  echo "❌ No data found"
fi
