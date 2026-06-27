# Baseline CPU miner image for the CPU Lottery Miner app.
#
# Built from cpuminer-multi (SHA256d + Stratum + cgminer-style --api-bind) with a
# generic x86-64 target, so the binary runs on ANY amd64 CPU — including
# constrained VMs without AVX, where the off-the-shelf cniweb/cpuminer-opt
# builds crash with SIGILL. Modern CPUs (AVX2/AVX512) get the much faster
# cniweb image instead; this is the universal fallback selected automatically
# by selectCpuMinerImage in internal/server/apps_cpuminer.go.
#
# The binary is installed as /usr/local/bin/cpuminer with NO ENTRYPOINT, so the
# app's compose command ("cpuminer --algo sha256d ...") works the same way it
# does for the cniweb image.
#
# Build & publish (one-time, on any amd64 host; must be a PUBLIC Docker Hub repo):
#   docker build -t jvx1971/cpu-lottery-miner:v1 \
#     lightningos-light/docker/cpu-lottery-miner
#   docker login
#   docker push jvx1971/cpu-lottery-miner:v1
#
# Quick local sanity check (should print a hashrate on any amd64):
#   docker run --rm jvx1971/cpu-lottery-miner:v1 \
#     cpuminer --algo sha256d --benchmark --api-bind 127.0.0.1:4048

FROM debian:bookworm-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
        git ca-certificates build-essential automake autoconf pkg-config \
        libcurl4-openssl-dev libjansson-dev libssl-dev libgmp-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
# tpruvot's fork (not lucasjones') ships the cgminer-style API (--api-bind) the
# app needs for live stats, and builds its binary already named "cpuminer".
RUN git clone --depth 1 https://github.com/tpruvot/cpuminer-multi.git .
# Generic x86-64 target (SSE2 baseline) for maximum compatibility — speed is
# irrelevant for a lottery miner; running everywhere is the point.
# LIBS forces libcurl/libcrypto onto the final link line — tpruvot's configure
# doesn't always add them, which breaks the link with undefined curl_*/SHA* refs.
RUN ./autogen.sh \
    && ./configure CFLAGS="-O2 -pipe" LIBS="-lcurl -lcrypto" \
    && make -j"$(nproc)" \
    && strip cpuminer

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        libcurl4 libjansson4 ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/cpuminer /usr/local/bin/cpuminer
WORKDIR /cpuminer
# No ENTRYPOINT on purpose: the compose command supplies "cpuminer <args>".
