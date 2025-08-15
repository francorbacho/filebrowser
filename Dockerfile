FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev git

WORKDIR /src
COPY . .

# Get git commit hash and build date
RUN GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown") && \
    BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) && \
    go build -ldflags "-X main.GitCommit=${GIT_COMMIT} -X main.BuildDate=${BUILD_DATE}" -o filebrowser main.go

FROM alpine:latest
COPY --from=builder /src/filebrowser /filebrowser
EXPOSE 8000
CMD ["/filebrowser"]

