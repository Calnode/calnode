#!/bin/sh
set -e

mkdir -p /data

if [ -n "$LITESTREAM_REPLICA_URL" ]; then
    # Restore the latest snapshot only if the DB file doesn't already exist.
    # -if-replica-exists prevents an error when the replica bucket is empty
    # (e.g. very first deploy).
    if [ ! -f /data/calnode.db ]; then
        litestream restore \
            -config /etc/litestream.yml \
            -if-replica-exists \
            /data/calnode.db
    fi

    # Start calnode under Litestream so WAL changes are streamed continuously.
    exec litestream replicate \
        -config /etc/litestream.yml \
        -exec "/calnode"
else
    # No replica configured — run directly (local dev or first-time setup).
    exec /calnode
fi
