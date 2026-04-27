#!/usr/bin/env bash
# jq_check.sh

ERROR=0
for file in "$@"; do
  if ! jq empty "$file" > /dev/null 2>&1; then
    echo "jq found errors in $file"
    ERROR=1
  fi
done

exit $ERROR
