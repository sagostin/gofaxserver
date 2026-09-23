# FreeSWITCH for gofaxserver — SIP / T.38 termination (mod_sofia + mod_spandsp).
#
# Building requires a SignalWire token (their apt repo is authenticated). The
# token is passed as a build secret and never lands in an image layer:
#
#   export SIGNALWIRE_TOKEN=your-token
#   docker compose -f docker-compose.full.yml build freeswitch
#   # or, by hand:
#   DOCKER_BUILDKIT=1 docker build \
#     --secret id=signalwire_token,env=SIGNALWIRE_TOKEN \
#     -f examples/freeswitch/Dockerfile -t gofaxserver-freeswitch:latest .
#
# A copy of the example configuration tree (examples/freeswitch/, seeded into
# volumes/freeswitch by `make fs-config`) is bind-mounted over /etc/freeswitch
# by docker-compose.full.yml at runtime — it is NOT baked in, so editing
# vars.xml / sofia.conf.xml only needs a container restart.

FROM debian:bookworm-slim

# Install the same package set documented for host installs (README /
# docs/INSTALLATION.md). The auth.conf carrying the token is removed again in
# the same layer so the secret does not persist in the image.
RUN --mount=type=secret,id=signalwire_token \
    apt-get update && \
    apt-get install -y --no-install-recommends gnupg2 wget ca-certificates && \
    TOKEN=$(cat /run/secrets/signalwire_token) && \
    wget --http-user=signalwire --http-password="$TOKEN" \
        -O /usr/share/keyrings/signalwire-freeswitch-repo.gpg \
        https://freeswitch.signalwire.com/repo/deb/debian-release/signalwire-freeswitch-repo.gpg && \
    echo "machine freeswitch.signalwire.com login signalwire password $TOKEN" > /etc/apt/auth.conf && \
    echo "deb [signed-by=/usr/share/keyrings/signalwire-freeswitch-repo.gpg] https://freeswitch.signalwire.com/repo/deb/debian-release/ bookworm main" \
        > /etc/apt/sources.list.d/freeswitch.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        freeswitch \
        freeswitch-mod-commands \
        freeswitch-mod-dptools \
        freeswitch-mod-event-socket \
        freeswitch-mod-sofia \
        freeswitch-mod-spandsp \
        freeswitch-mod-tone-stream \
        freeswitch-mod-db \
        freeswitch-mod-syslog \
        freeswitch-mod-logfile && \
    rm -f /etc/apt/auth.conf && \
    rm -rf /var/lib/apt/lists/*

# Persistent state: /var/lib/freeswitch holds mod_db (sofia registrations and
# gofaxserver's softmodem-fallback flags) — mount it as a volume.
# /var/log/freeswitch holds freeswitch.log / sip traces.
VOLUME ["/var/lib/freeswitch", "/var/log/freeswitch"]

# Runs in the foreground as root (stock Debian behaviour). Root reads the
# 0640 gateway XML files gofaxserver writes into /etc/freeswitch/gateways
# without any extra permission plumbing.
CMD ["/usr/bin/freeswitch", "-nonat", "-nc", "-nf"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["/usr/bin/fs_cli", "-x", "status"]
