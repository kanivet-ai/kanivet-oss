#!/bin/bash
# Configure git to use .githooks directory
git config core.hooksPath .githooks
echo "✅ Git hooks enabled - direct pushes to main/master are now blocked"
