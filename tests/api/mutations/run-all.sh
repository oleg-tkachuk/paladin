#!/usr/bin/env bash

set -eo pipefail

echo "========================================"
echo " Running All gRPC E2E Tests"
echo "========================================"

# Run happy paths
if [ -f "run_happy.sh" ]; then
    ./run_happy.sh
fi

# Run mutated paths
if [ -f "run_mutations.sh" ]; then
    ./run_mutations.sh
fi

echo "========================================"
echo " All tests completed successfully!"
echo "========================================"
