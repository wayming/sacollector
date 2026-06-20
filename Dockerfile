FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /collector .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /collector .
COPY web/dist ./web/dist
EXPOSE 8080
ENTRYPOINT ["./collector"]
CMD ["-redis-addr", "redis:6379", "-output", "/app/output", "-port", "8080"]
