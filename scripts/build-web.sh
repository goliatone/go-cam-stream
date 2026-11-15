#!/bin/sh
set -eu
cd "$(dirname "$0")/../frontend"
npm ci
npm run build
node scripts/embed.mjs
