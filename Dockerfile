FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY . .
RUN go build -o sfb main.go

FROM alpine:latest
COPY --from=builder /src/sfb /sfb
CMD ["/sfb"]

