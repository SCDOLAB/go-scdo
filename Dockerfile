# Go builder container. Default image is Scdo_V2.0.0 (no GPU library required).
FROM golang:1.22-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev make \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY . .

RUN make node client

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /src/build /scdo

ENV PATH /scdo:$PATH

RUN chmod +x /scdo/node /scdo/client

EXPOSE 8027 8037 8057

# start a node with your 'config.json' file, this file must be external from a volume
# For example:
#   docker run -v <your config path>:/scdo/config:ro -it scdo node start -c /scdo/config/configfile
