# Single-container phebs image: the official release tarball + git + a pinned SurrealDB.
#
# linux/amd64 only — the official releases currently publish only that
# architecture:
#
#   docker build --platform linux/amd64 -t phebs:0.3.5 .

# ---------------------------------------------------------------------------
# Stage 1: download and verify both release archives.
# Kept in its own stage so neither the download tools nor the archives
# end up in the final image.
# aria2 with multiple connections because GitHub release downloads
# rate-limit single connections noticeably.
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim AS fetch

# When bumping the version, change the version and the checksum together.
# The phebs checksum comes from SHA256SUMS on the release page;
# the SurrealDB checksum comes from .surrealdb-linux-amd64.sha256 at the
# repository root.
ARG PHEBS_VERSION=0.3.5
ARG PHEBS_SHA256=165a2f43e8418ec4e7b18524598dedef185424dfdb3c8edc2255dceb87712a6e
ARG SURREALDB_VERSION=3.2.0
ARG SURREALDB_SHA256=9c0a9ae29444f3b144a1261fc923116b0e10a3cbadc478cabc9009b3beb9bb3a

RUN apt-get update \
 && apt-get install -y --no-install-recommends aria2 ca-certificates \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /dl

RUN aria2c --max-connection-per-server=16 --split=16 --min-split-size=1M \
        --file-allocation=none --console-log-level=warn --summary-interval=0 \
        --max-tries=20 --retry-wait=3 \
        --out=surreal.tgz \
        "https://github.com/surrealdb/surrealdb/releases/download/v${SURREALDB_VERSION}/surreal-v${SURREALDB_VERSION}.linux-amd64.tgz" \
 && echo "${SURREALDB_SHA256}  surreal.tgz" | sha256sum --check -

RUN aria2c --max-connection-per-server=16 --split=16 --min-split-size=1M \
        --file-allocation=none --console-log-level=warn --summary-interval=0 \
        --max-tries=20 --retry-wait=3 \
        --out=phebs.tar.gz \
        "https://github.com/bmeddeb/phebs/releases/download/v${PHEBS_VERSION}/phebs-v${PHEBS_VERSION}-linux-amd64.tar.gz" \
 && echo "${PHEBS_SHA256}  phebs.tar.gz" | sha256sum --check -

# ---------------------------------------------------------------------------
# Final image
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim

# phebs shells out to git to clone and fetch repositories; curl is only for
# health checks and debugging.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git curl \
 && rm -rf /var/lib/apt/lists/*

# The SurrealDB archive is a single surreal executable.
COPY --from=fetch /dl/surreal.tgz /tmp/surreal.tgz
RUN tar -xzf /tmp/surreal.tgz -C /usr/local/bin \
 && rm /tmp/surreal.tgz \
 && chmod 0755 /usr/local/bin/surreal

# The phebs archive unpacks to runtime/ and packs/. The evidence-pack
# signature records bind to that executable and every file under packs/, so
# unpack it intact next to the executable and never modify or split it. The
# official install notes confirm a symlink pointing at the executable is
# safe; phebs resolves the real path at startup.
COPY --from=fetch /dl/phebs.tar.gz /tmp/phebs.tar.gz
RUN mkdir -p /opt/phebs \
 && tar -xzf /tmp/phebs.tar.gz --strip-components=1 -C /opt/phebs \
 && rm /tmp/phebs.tar.gz \
 && ln -s /opt/phebs/runtime/phebs /usr/local/bin/phebs

# Pre-create the data directory for the runtime user. Mounting a fresh empty
# volume inherits this directory's owner; otherwise the mount point is
# root-owned and the non-root phebs cannot write to it.
RUN mkdir -p /data /etc/phebs \
 && chown 10001:10001 /data

# The image has no passwd entry for UID 10001; give git a writable HOME.
ENV HOME=/tmp

USER 10001:10001

EXPOSE 3070

# Kubernetes ignores the Docker HEALTHCHECK (probes live in the Deployment);
# this one only helps plain `docker run`.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD ["curl", "--fail", "--silent", "http://127.0.0.1:3070/api/health"]

# exec form so phebs runs as PID 1 and receives SIGTERM directly;
# it requeues in-flight jobs and stops the SurrealDB child it supervises.
ENTRYPOINT ["/opt/phebs/runtime/phebs", "serve"]
CMD ["-config", "/etc/phebs/phebs.yaml"]
