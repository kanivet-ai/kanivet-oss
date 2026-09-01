#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Parse arguments
SKIP_SIGN=false
for arg in "$@"; do
    if [ "$arg" = "--skip-sign" ]; then
        SKIP_SIGN=true
    fi
done

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if gh CLI is installed
if ! command -v gh &> /dev/null; then
    print_error "GitHub CLI (gh) is not installed. Please install it first:"
    print_error "brew install gh"
    exit 1
fi

# Check if logged in to GitHub
if ! gh auth status &> /dev/null; then
    print_error "Not logged in to GitHub. Please run: gh auth login"
    exit 1
fi

# Generate release version with current timestamp
RELEASE_VERSION=$(date +"%Y.%m.%d-%H%M%S")
RELEASE_TAG="v${RELEASE_VERSION}"

print_status "Creating release: ${RELEASE_TAG}"

# Change to frontend directory
print_status "Changing to frontend directory..."
cd frontend

# Clean previous builds
print_status "Cleaning previous builds..."
rm -rf dist/
rm -rf build/

# Build the application
if [ "$SKIP_SIGN" = true ]; then
    print_status "Building the application (skipping signing and notarization)..."
    CSC_IDENTITY_AUTO_DISCOVERY=false SKIP_NOTARIZATION=true npm run dist:mac
else
    print_status "Building the application..."
    npm run dist:mac
fi

# Check if DMG was created successfully
DMG_FILE="dist/kanivet-1.0.0-arm64.dmg"
ZIP_FILE="dist/kanivet-1.0.0-arm64-mac.zip"

if [ ! -f "$DMG_FILE" ]; then
    print_error "DMG file not found: $DMG_FILE"
    exit 1
fi

if [ ! -f "$ZIP_FILE" ]; then
    print_error "ZIP file not found: $ZIP_FILE"
    exit 1
fi

print_success "Build completed successfully!"

# Verify backend binary is included in the app
print_status "Verifying backend binary is included in the application..."
APP_PATH="dist/mac-arm64/kanivet.app"
BACKEND_BINARY_PATH="${APP_PATH}/Contents/Resources/resources/kanivet-backend"

if [ -d "$APP_PATH" ]; then
    if [ -f "$BACKEND_BINARY_PATH" ]; then
        BACKEND_SIZE=$(du -h "$BACKEND_BINARY_PATH" | cut -f1)
        print_success "Backend binary found in app bundle (size: $BACKEND_SIZE)"
    else
        print_error "Backend binary NOT found in app bundle!"
        print_error "Expected at: $BACKEND_BINARY_PATH"
        print_error "Build may be incomplete. The app will not function properly without the backend."

        # Check if backend was built
        if [ -f "resources/kanivet-backend" ]; then
            print_warning "Backend binary exists in resources/ but was not packaged into the app."
            print_warning "This suggests a problem with the electron-builder configuration."
        else
            print_error "Backend binary not found in resources/ directory either."
            print_error "The build:backend step may have failed."
        fi

        exit 1
    fi
else
    print_error "Application bundle not found at: $APP_PATH"
    exit 1
fi

# Get file sizes for display
DMG_SIZE=$(du -h "$DMG_FILE" | cut -f1)
ZIP_SIZE=$(du -h "$ZIP_FILE" | cut -f1)

print_status "DMG size: $DMG_SIZE"
print_status "ZIP size: $ZIP_SIZE"

# Create GitHub release
print_status "Creating GitHub release..."

RELEASE_NOTES="## Release ${RELEASE_TAG}

Automated release created on $(date '+%B %d, %Y at %H:%M:%S %Z')

### Assets
- **kanivet-1.0.0-arm64.dmg** (${DMG_SIZE}) - macOS DMG installer for Apple Silicon
- **kanivet-1.0.0-arm64-mac.zip** (${ZIP_SIZE}) - macOS ZIP archive for Apple Silicon

### Installation
1. Download the DMG file
2. Open the DMG and drag the app to Applications folder
3. If you encounter security warnings, go to System Preferences > Security & Privacy and allow the app to run

Built with:
- Electron $(npm list electron --depth=0 2>/dev/null | grep electron | sed 's/.*electron@//' | sed 's/ .*//')
- React $(npm list react --depth=0 2>/dev/null | grep react | sed 's/.*react@//' | sed 's/ .*//')
- Go backend"

# Create the release
gh release create "$RELEASE_TAG" \
    --title "Release $RELEASE_TAG" \
    --notes "$RELEASE_NOTES" \
    --draft=false \
    --prerelease=false

print_success "GitHub release created: $RELEASE_TAG"

# Upload assets
print_status "Uploading DMG file..."
gh release upload "$RELEASE_TAG" "$DMG_FILE" --clobber

print_status "Uploading ZIP file..."
gh release upload "$RELEASE_TAG" "$ZIP_FILE" --clobber

print_success "Assets uploaded successfully!"

# Get release URL
RELEASE_URL=$(gh release view "$RELEASE_TAG" --json url -q .url)

print_success "Release completed successfully! 🎉"
print_success "Release URL: $RELEASE_URL"

# Optional: Open the release in browser
read -p "Open release in browser? (y/n): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    open "$RELEASE_URL"
fi
