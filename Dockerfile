FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod ./
# go.sum will be generated after `go mod tidy`
COPY . .
RUN go mod tidy && go build -o planasonix-mcp .

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/planasonix-mcp .

EXPOSE 8080

ENV PORT=8080 \
    PLANASONIX_API_URL=http://backend-smb:8080 \
    DATABASE_URL=""

ENTRYPOINT ["./planasonix-mcp"]
