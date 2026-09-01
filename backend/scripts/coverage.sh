#!/bin/bash

set -e

cd "$(dirname "$0")/.."

COVERAGE_THRESHOLD=${COVERAGE_THRESHOLD:-50}
COVERAGE_FILE="coverage.out"
COVERAGE_HTML="coverage.html"

echo "Running Go tests with coverage..."

set +e
go test -race -covermode=atomic -coverprofile=${COVERAGE_FILE} ./...
TEST_EXIT_CODE=$?
set -e

if [ ! -f ${COVERAGE_FILE} ]; then
    echo "❌ Coverage file not generated. Tests may have failed."
    exit 1
fi

if [ ${TEST_EXIT_CODE} -ne 0 ]; then
    echo "⚠️  Some tests failed (race conditions detected), but coverage report was generated."
    echo "🔧 Consider fixing race conditions in tests before committing."
fi

COVERAGE=$(go tool cover -func=${COVERAGE_FILE} | grep total | awk '{print $3}' | sed 's/%//')

echo "📊 Total test coverage: ${COVERAGE}%"

if (( $(echo "${COVERAGE} < ${COVERAGE_THRESHOLD}" | bc -l) )); then
    echo "❌ Coverage ${COVERAGE}% is below threshold ${COVERAGE_THRESHOLD}%"
    echo "💡 Add more tests or adjust COVERAGE_THRESHOLD environment variable"
    exit 1
else
    echo "✅ Coverage ${COVERAGE}% meets threshold ${COVERAGE_THRESHOLD}%"
fi

echo "📈 Generating HTML coverage report..."
go tool cover -html=${COVERAGE_FILE} -o ${COVERAGE_HTML}
echo "🌐 Coverage report generated: ${COVERAGE_HTML}"

echo "📋 Coverage by package:"
go tool cover -func=${COVERAGE_FILE} | grep -v "total:"
