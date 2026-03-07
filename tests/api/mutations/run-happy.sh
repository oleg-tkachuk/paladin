#!/usr/bin/env bash

set -eo pipefail

echo "========================================"
echo " Running Happy Path Tests"
echo "========================================"

# Find all happy.sh scripts in cases directory and execute them
find ./cases -name "happy.sh" -type f | sort | while read -r test_script; do
    echo "-> Executing: $test_script"
    bash "$test_script"
done

echo "========================================"
echo " Happy Path Tests Complete!"
echo "========================================"
