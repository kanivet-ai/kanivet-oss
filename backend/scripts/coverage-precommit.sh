#!/bin/bash

set -e

cd "$(dirname "$0")/.."

COVERAGE_FILE="coverage.out"

echo "🔍 Running pre-commit coverage check..."

go test -covermode=atomic -coverprofile=${COVERAGE_FILE} ./... > /dev/null 2>&1

if [ ! -f ${COVERAGE_FILE} ]; then
    echo "❌ Coverage file not generated. Tests may have failed."
    exit 1
fi

COVERAGE=$(go tool cover -func=${COVERAGE_FILE} | grep total | awk '{print $3}' | sed 's/%//')

echo "📊 Total test coverage: ${COVERAGE}%"
echo "✅ All tests passed!"

rm -f ${COVERAGE_FILE}
echo "🎉 Pre-commit coverage check passed!"
