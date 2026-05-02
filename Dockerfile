# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /bin/hf-sync .

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates git-lfs
COPY --from=builder /bin/hf-sync /usr/local/bin/hf-sync

ENTRYPOINT ["hf-sync"]
