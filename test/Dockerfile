FROM golang:1.27
RUN apt-get update && apt-get install -y --no-install-recommends bubblewrap git && rm -rf /var/lib/apt/lists/* \
    && useradd -m e2e && mkdir -p /go/pkg/mod && chown -R e2e /go
USER e2e
WORKDIR /src
