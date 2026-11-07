#!/bin/sh
# PostToolUse hook: gofmt Go files right after an agent edits them.
# Receives the tool call as JSON on stdin.
command -v jq >/dev/null 2>&1 || exit 0
file=$(jq -r '.tool_input.file_path // empty')
case "$file" in
  *.go) [ -f "$file" ] && gofmt -w "$file" ;;
esac
exit 0
