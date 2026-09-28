#!/bin/sh
# Copies the contract fixtures the app consumes out of the CLI's own testdata.
# Run it after the contract changes; CI runs it and fails on a diff, so a payload
# that moves under the app is caught in the product's CI and not in a user's hands.
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
from="$root/cmd/infercat/testdata/contract"
into="$root/apps/macos/Resources/Fixtures/contract"

mkdir -p "$into"
rm -f "$into"/*.json

for name in \
  status.empty status.populated \
  keys.list.empty keys.list.populated \
  keys.get.empty keys.get.populated \
  keys.add.empty keys.add.populated \
  keys.rotate.empty keys.rotate.populated \
  keys.limits.empty keys.limits.populated \
  keys.pause.empty keys.pause.populated \
  keys.resume.empty keys.resume.populated \
  keys.revoke.empty keys.revoke.populated \
  service.status.empty service.status.populated \
  service.install.empty service.install.populated \
  service.start.empty service.start.populated \
  service.stop.empty service.stop.populated \
  service.restart.empty service.restart.populated \
  settings.set.empty settings.set.populated \
  usage.empty usage.populated \
  console.open.empty console.open.populated \
  version.empty version.populated \
  watch.hello watch.status.empty watch.status.populated \
  watch.event watch.dropped watch.gone \
  error
do
  cp "$from/$name.json" "$into/$name.json"
done

echo "vendored $(ls "$into" | wc -l | tr -d ' ') contract fixtures into apps/macos/Resources/Fixtures/contract"
