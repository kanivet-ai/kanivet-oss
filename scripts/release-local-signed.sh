#!/bin/bash
set -e

echo "🚀 Starting local release process with code signing..."

cd "$(dirname "$0")/.."

FRONTEND_DIR="frontend"

if [ ! -d "$FRONTEND_DIR" ]; then
  echo "❌ Frontend directory not found!"
  exit 1
fi

cd "$FRONTEND_DIR"

echo "🔐 Checking for code signing certificate..."
IDENTITY=$(security find-identity -v -p codesigning | grep "Developer ID Application" | head -n 1)
if [ -z "$IDENTITY" ]; then
  echo "⚠️ WARNING: No Developer ID Application certificate found in keychain"
  echo "The app will be signed ad-hoc (without a certificate)"
  read -p "Continue anyway? (y/n) " -n 1 -r
  echo
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    exit 1
  fi
else
  echo "✅ Found certificate: $IDENTITY"
fi

if [ -n "$APPLE_ID" ] && [ -n "$APPLE_APP_SPECIFIC_PASSWORD" ] && [ -n "$APPLE_TEAM_ID" ]; then
  echo "✅ Notarization credentials found"
else
  echo "ℹ️ Notarization credentials not set. The app will be signed but not notarized."
  echo "Set APPLE_ID, APPLE_APP_SPECIFIC_PASSWORD, and APPLE_TEAM_ID to enable notarization"
fi

echo "📦 Installing dependencies..."
npm ci --legacy-peer-deps

if ! npm list @electron/notarize > /dev/null 2>&1; then
  echo "📦 Installing @electron/notarize..."
  npm install --save-dev @electron/notarize
fi

echo "🏗️ Building app (frontend + backend)..."
npm run dist:mac

APP_PATH=$(find dist -name "*.app" -type d | head -n 1)
DMG_FILE=$(ls dist/*.dmg 2>/dev/null | head -n 1)
ZIP_FILE=$(find dist -name "*-mac.zip" 2>/dev/null | head -n 1)

if [ -d "$APP_PATH" ]; then
  echo "✅ App built successfully at: $APP_PATH"

  echo "📁 Creating additional ZIP file for backup..."
  cd $(dirname "$APP_PATH")
  zip -r -y "kanivet-mac-signed.zip" $(basename "$APP_PATH")
  cd -

  ZIP_FILE=$(find dist -name "kanivet-mac-signed.zip" 2>/dev/null | head -n 1)
fi

if [ -f "$DMG_FILE" ] || [ -f "$ZIP_FILE" ]; then
  TIMESTAMP=$(date +%Y%m%d-%H%M%S)
  TAG_NAME="v${TIMESTAMP}"
  RELEASE_NAME="Build ${TIMESTAMP}"

  cat > release_notes.md << 'EOF'
## Installation Instructions for macOS

This app is code signed with an Apple Developer certificate.

### For DMG file (Recommended):
1. Download and mount the DMG
2. Drag kanivet to your Applications folder
3. Open the app normally

### For ZIP file (Alternative):
1. Download `kanivet-mac-signed.zip`
2. Extract the ZIP file
3. Move kanivet.app to Applications
4. Open the app normally

### If you see a security warning:
- Go to System Settings > Privacy & Security
- Click "Open Anyway" next to the app name

---
Build from local release script
EOF

  echo ""
  echo "📋 Release Summary:"
  echo "==================="
  echo "Tag: $TAG_NAME"
  echo "Title: $RELEASE_NAME"
  [ -f "$DMG_FILE" ] && echo "DMG: $(basename $DMG_FILE)"
  [ -f "$ZIP_FILE" ] && echo "ZIP: $(basename $ZIP_FILE)"
  if [ -n "$IDENTITY" ]; then
    echo "Code Signed: ✅ Yes"
  else
    echo "Code Signed: ⚠️ Ad-hoc"
  fi
  if [ -n "$APPLE_ID" ] && [ -n "$APPLE_APP_SPECIFIC_PASSWORD" ] && [ -n "$APPLE_TEAM_ID" ]; then
    echo "Notarized: ✅ Yes"
  else
    echo "Notarized: ❌ No"
  fi
  echo ""

  read -p "🚀 Create GitHub release? (y/n) " -n 1 -r
  echo
  if [[ $REPLY =~ ^[Yy]$ ]]; then
    FILES=""
    [ -f "$DMG_FILE" ] && FILES="$FILES $DMG_FILE"
    [ -f "$ZIP_FILE" ] && FILES="$FILES $ZIP_FILE"

    gh release create "$TAG_NAME" \
      --title "$RELEASE_NAME" \
      --notes-file release_notes.md \
      --prerelease \
      $FILES

    echo "✅ Release created: $TAG_NAME"
    [ -f "$DMG_FILE" ] && echo "✅ DMG uploaded: $(basename $DMG_FILE)"
    [ -f "$ZIP_FILE" ] && echo "✅ ZIP uploaded: $(basename $ZIP_FILE)"
  else
    echo "ℹ️ Skipping GitHub release creation"
    echo "Build artifacts are in: $FRONTEND_DIR/dist/"
  fi

  rm -f release_notes.md
else
  echo "❌ No DMG or ZIP file found to upload"
  exit 1
fi

echo "✅ Release process complete!"
