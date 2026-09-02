FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /app/api-gateway-bin .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/api-gateway-bin .
EXPOSE 8080
CMD ["./api-gateway-bin"]
