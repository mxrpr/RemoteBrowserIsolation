#!/usr/bin/env bash
# Runs all .NET test projects in the repo.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

mapfile -t TEST_PROJECTS < <(find . -not -path "*/bin/*" -not -path "*/obj/*" -iname "*Tests.csproj")

if [ ${#TEST_PROJECTS[@]} -eq 0 ]; then
    echo "No test projects found (looked for *Tests.csproj)."
    exit 0
fi

for proj in "${TEST_PROJECTS[@]}"; do
    echo "Running tests: $proj"
    dotnet test "$proj"
done
