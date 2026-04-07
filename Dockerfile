FROM golang:1.23-alpine AS builder

ARG VERSION=dev

WORKDIR /app
COPY go.mod ./
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o planasonix-mcp .

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/planasonix-mcp .

EXPOSE 8080

ENV PORT=8080 \
    PLANASONIX_API_URL=http://backend-smb:8080 \
    DATABASE_URL=""

ENTRYPOINT ["./planasonix-mcp"]
