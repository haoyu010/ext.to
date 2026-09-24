#!/bin/sh
# Prepares the data volume, then drops privileges to PUID:PGID.
#
# Bind mounts keep the host's ownership, so a container running as an
# arbitrary internal user often cannot write to /data. Fixing ownership here
# and then dropping privileges is what NAS users expect (PUID/PGID), and it
# keeps the process itself unprivileged.
set -eu

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"
DATA_DIR="${DATA_DIR:-/data}"

# Only root can chown; when the container is started with an explicit --user
# this block is skipped and the caller is responsible for permissions.
if [ "$(id -u)" = "0" ]; then
    if ! getent group "$PGID" >/dev/null 2>&1; then
        addgroup -g "$PGID" app >/dev/null 2>&1 || true
    fi
    if ! getent passwd "$PUID" >/dev/null 2>&1; then
        adduser -D -u "$PUID" -G "$(getent group "$PGID" | cut -d: -f1)" app >/dev/null 2>&1 || true
    fi

    mkdir -p "$DATA_DIR"
    # Recurse so a pre-existing file written by a different uid is fixed too.
    chown -R "$PUID:$PGID" "$DATA_DIR" 2>/dev/null || \
        echo "warning: could not chown $DATA_DIR; continuing anyway" >&2

    exec su-exec "$PUID:$PGID" /usr/local/bin/extto "$@"
fi

exec /usr/local/bin/extto "$@"
