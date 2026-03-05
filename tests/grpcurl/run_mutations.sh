#!/usr/bin/env bash

set -e # Do not fail fast on mutated tests, we want to run them all
# Wait, let's actually fail if a mutation test fails its assertion
set -eo pipefail

echo "========================================"
echo " Running Mutated (Negative) Tests"
echo "========================================"

# Find all mutations.sh scripts in cases directory and execute them
find ./cases -name "mutations.sh" -type f | sort | while read -r test_script; do
    echo "-> Executing: $test_script"
    bash "$test_script"
done

echo "========================================"
echo " Mutated Tests Complete!"
echo "========================================"
