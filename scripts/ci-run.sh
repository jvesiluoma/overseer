#!/usr/bin/env bash
# CI helper: runs a command and, if it fails, posts the end of its output as an error annotation,
# so the failure is readable from the run summary.   scripts/ci-run.sh "<title>" <command…>
title="$1"; shift
log="$(mktemp)"
"$@" 2>&1 | tee "$log"
status=${PIPESTATUS[0]}
if [[ $status -ne 0 ]]; then
  tail -n 80 "$log" | sed -e 's/%/%25/g' -e 's/\r//g' | awk 'BEGIN{ORS="%0A"} {print}' > "$log.tail"
  echo "::error title=$title::$(cat "$log.tail")"
fi
rm -f "$log" "$log.tail"
exit "$status"
