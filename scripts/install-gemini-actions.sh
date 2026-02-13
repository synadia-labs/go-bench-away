#!/bin/bash
# install-gemini-actions.sh
#
# Description:
#   Copies Gemini GitHub Actions workflows and configurations from the current
#   repository (source) to a target repository. It handles dependency checks
#   and prevents accidental overwrites of existing workflows.
#
# Usage:
#   ./scripts/install-gemini-actions.sh <target-repo-path>
#
# Example:
#   ./scripts/install-gemini-actions.sh ../my-new-project

set -e

SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_LIB="$1"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

log_info() { printf "%b[INFO]%b %s\n" "${GREEN}" "${NC}" "$1"; }
log_warn() { printf "%b[WARN]%b %s\n" "${YELLOW}" "${NC}" "$1"; }
log_error() { printf "%b[ERROR]%b %s\n" "${RED}" "${NC}" "$1"; }

if [ -z "$TARGET_LIB" ]; then
  log_error "Usage: $0 <target-repo-path>"
  exit 1
fi

if [ ! -d "$TARGET_LIB" ]; then
  log_error "Target directory does not exist: $TARGET_LIB"
  exit 1
fi

TARGET_WORKFLOWS="$TARGET_LIB/.github/workflows"
mkdir -p "$TARGET_WORKFLOWS"

log_info "Installing Gemini Actions from $SOURCE_DIR to $TARGET_WORKFLOWS"

# List of Gemini files to copy
FILES=(
  ".github/workflows/gemini-dispatch.yml"
  ".github/workflows/gemini-invoke.yml"
  ".github/workflows/gemini-invoke.toml"
  ".github/workflows/gemini-review.yml"
  ".github/workflows/gemini-review.toml"
  ".github/workflows/gemini-triage.yml"
  ".github/workflows/gemini-triage.toml"
  ".github/workflows/gemini-unit-gen.yml"
  ".github/workflows/gemini-unit-gen.toml"
  ".github/workflows/gemini-shrink-optimizer.yml"
  ".github/workflows/gemini-shrink-optimizer.toml"
  ".github/workflows/gemini-architect.toml"
  ".github/workflows/gemini-algorithm.toml"
)

SUCCESS_COUNT=0
SKIP_COUNT=0

for FILE in "${FILES[@]}"; do
  SRC_PATH="$SOURCE_DIR/$FILE"
  DEST_PATH="$TARGET_LIB/$FILE"
  
  if [ ! -f "$SRC_PATH" ]; then
    log_warn "Source file not found (skipping): $SRC_PATH"
    continue
  fi

  if [ -f "$DEST_PATH" ]; then
    log_warn "File exists at target: $DEST_PATH"
    
    # Only prompt if we are in an interactive terminal
    if [ -t 0 ]; then
      read -p "  Overwrite? [y/N] " -n 1 -r
      echo
      if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Skipping $FILE"
        ((SKIP_COUNT++))
        continue
      fi
    else
      log_info "Non-interactive mode: skipping existing file $FILE"
      ((SKIP_COUNT++))
      continue
    fi
  fi

  cp "$SRC_PATH" "$DEST_PATH"
  log_info "Installed $FILE"
  ((SUCCESS_COUNT++))
done

echo ""
log_info "Installation Complete!"
log_info "  Installed: $SUCCESS_COUNT"
log_info "  Skipped:   $SKIP_COUNT"

if [ "$SUCCESS_COUNT" -gt 0 ]; then
  echo ""
  printf "%bNext Steps:%b\n" "${YELLOW}" "${NC}"
  echo "1. Navigate to your target repository: cd \"$TARGET_LIB\""
  echo "2. Add the required secrets (GEMINI_API_KEY, etc.) to your repository settings."
  echo "3. Review the 'gemini-dispatch.yml' triggers to match your workflow needs."
  echo "4. Commit and push the new workflow files."
fi
