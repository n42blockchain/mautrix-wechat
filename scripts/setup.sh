#!/bin/bash
# Setup script for mautrix-wechat bridge
# Creates necessary directories and generates random tokens

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "Setting up mautrix-wechat bridge..."

# Create directories
mkdir -p "$PROJECT_DIR/secrets"
mkdir -p "$PROJECT_DIR/logs"
mkdir -p "$PROJECT_DIR/configs"

# Generate random tokens if they don't exist
generate_token() {
    local file="$1"
    if [ ! -f "$file" ]; then
        openssl rand -hex 32 > "$file"
        echo "Generated: $file"
    else
        echo "Exists:    $file"
    fi
}

generate_token "$PROJECT_DIR/secrets/db_password"
generate_token "$PROJECT_DIR/secrets/as_token"
generate_token "$PROJECT_DIR/secrets/hs_token"

# Generate example config if it doesn't exist
CONFIG="$PROJECT_DIR/configs/config.yaml"
if [ ! -f "$CONFIG" ]; then
    cd "$PROJECT_DIR"
    go run ./cmd/mautrix-wechat -generate-config > "$CONFIG"

    # Replace tokens with generated values
    AS_TOKEN=$(cat "$PROJECT_DIR/secrets/as_token")
    HS_TOKEN=$(cat "$PROJECT_DIR/secrets/hs_token")
    DB_PASS=$(cat "$PROJECT_DIR/secrets/db_password")

    if [[ "$OSTYPE" == "darwin"* ]]; then
        sed -i '' "s/CHANGE_ME_AS_TOKEN/$AS_TOKEN/" "$CONFIG"
        sed -i '' "s/CHANGE_ME_HS_TOKEN/$HS_TOKEN/" "$CONFIG"
        sed -i '' "s/password/$DB_PASS/" "$CONFIG"
    else
        sed -i "s/CHANGE_ME_AS_TOKEN/$AS_TOKEN/" "$CONFIG"
        sed -i "s/CHANGE_ME_HS_TOKEN/$HS_TOKEN/" "$CONFIG"
        sed -i "s/password/$DB_PASS/" "$CONFIG"
    fi

    echo "Generated: $CONFIG"
else
    echo "Exists:    $CONFIG"
fi

# Generate registration.yaml
REG="$PROJECT_DIR/configs/registration.yaml"
if [ ! -f "$REG" ]; then
    cd "$PROJECT_DIR"
    go run ./cmd/mautrix-wechat -config "$CONFIG" -generate-registration > "$REG"
    echo "Generated: $REG"
else
    echo "Exists:    $REG"
fi

echo ""
echo "Setup complete! Next steps:"
echo "  1. Edit configs/config.yaml with your settings"
echo "  2. Copy configs/registration.yaml to your Matrix homeserver"
echo "  3. Add the registration to your homeserver config"
echo "  4. Run: docker compose up -d"
