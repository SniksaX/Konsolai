#!/bin/sh
# Any uid can run the image. /tmp/home is mode 1777. This script does not need root.
set -eu
base=/tmp/home
if [ ! -d "$base" ]; then
	mkdir -p "$base"
fi
uid=$(id -u)
home="$base/u${uid}"
mkdir -p "$home/.config" "$home/.cache" "$home/run"
chmod 700 "$home" "$home/.config" "$home/.cache" "$home/run"
export HOME="$home"
export XDG_CONFIG_HOME="$home/.config"
export XDG_CACHE_HOME="$home/.cache"
export XDG_RUNTIME_DIR="$home/run"
exec /usr/local/bin/konsolai "$@"
