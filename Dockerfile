# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN make build

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache \
    erofs-utils \
    e2fsprogs \
    util-linux \
    && rm -rf /var/cache/apk/*

COPY --from=builder /src/bin/erofs-snapshotter /usr/local/bin/

VOLUME ["/var/lib/erofs-snapshotter"]

ENTRYPOINT ["/usr/local/bin/erofs-snapshotter"]
CMD ["--help"]
