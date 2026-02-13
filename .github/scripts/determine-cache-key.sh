#!/usr/bin/env bash
# Determine cache key that rotates monthly (year-month format)
# Outputs to GITHUB_OUTPUT in GitHub Actions context

set -e

# Use year-month as cache key, rotating monthly
echo "value=$(date +%Y-%m)" >> "$GITHUB_OUTPUT"
