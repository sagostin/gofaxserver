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

# Pinned versions (linux/amd64 package strings — the SignalWire repo uses a
# different version suffix on other architectures).
#
# FreeSWITCH is pinned to 1.11.1: it is the newest release whose
# freeswitch-mod-spandsp package still links against libspandsp3 (spandsp
# 3.0.0). From 1.11.2 onwards mod_spandsp links libspandsp4 (spandsp 3.1.x),
# which contains a fax page-header rendering regression: commit
# freeswitch/spandsp@d9681c3 re-introduced x_repeats in
# header_row_read_handler() (src/t4_tx.c), doubling the pixel width of the
# header line at standard/fine resolution so the header looks stretched.
# Upstream issue: https://github.com/freeswitch/spandsp/issues/129 (open).
# Revisit this pin once #129 is fixed and shipped in a libspandsp4 build.
ARG FS_VERSION=1.11.1-release-26476433763-c2c59645f6~bookworm~amd64-1~bookworm+1
ARG SPANDSP_VERSION=3.0.0-16343706291-e29ef78944~bookworm

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
        freeswitch=${FS_VERSION} \
        libfreeswitch1=${FS_VERSION} \
        freeswitch-mod-commands=${FS_VERSION} \
        freeswitch-mod-dptools=${FS_VERSION} \
        freeswitch-mod-event-socket=${FS_VERSION} \
        freeswitch-mod-sofia=${FS_VERSION} \
        freeswitch-mod-spandsp=${FS_VERSION} \
        freeswitch-mod-tone-stream=${FS_VERSION} \
        freeswitch-mod-db=${FS_VERSION} \
        freeswitch-mod-syslog=${FS_VERSION} \
        freeswitch-mod-logfile=${FS_VERSION} \
        libspandsp3=${SPANDSP_VERSION} && \
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
