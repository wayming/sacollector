# Stage 1: Build frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binaries
FROM golang:1.25-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /collector .
RUN CGO_ENABLED=0 go build -o /mcp-server ./cmd/mcp-server/

# Stage 3: Runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=go-builder /collector .
COPY --from=go-builder /mcp-server .
COPY --from=frontend-builder /src/web/dist ./web/dist
EXPOSE 8080 8081
ENTRYPOINT ["./collector"]
CMD ["-redis-addr", "redis:6379", "-output", "/app/output", "-port", "8080"]
