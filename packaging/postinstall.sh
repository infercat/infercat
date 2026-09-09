#!/bin/sh
# Installation must never start a host on behalf of a user.
printf '%s\n' 'To start Infercat, run as your regular user:' \
  '  systemctl --user daemon-reload' \
  '  systemctl --user enable --now infercat' \
  'Start your inference engine first. See /usr/share/doc/infercat/LINUX.md.'
