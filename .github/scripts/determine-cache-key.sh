#!/usr/bin/env bash
# Determine cache key that changes every 28 days
# Outputs to GITHUB_OUTPUT in GitHub Actions context
#
# Usage: determine-cache-key.sh

set -e

# Get the day of year (1-366)
DAY_OF_YEAR=$(date +%-j)

# Use modulo to create a key that changes every 28 days
if [ $((DAY_OF_YEAR % 28)) -eq 0 ]; then
  # On the 28th, 56th, 84th... day of the year, use a date-based key
  echo "value=$(date +%Y-%m-%d)" >> "$GITHUB_OUTPUT"
else
  echo "value=latest" >> "$GITHUB_OUTPUT"
fi
