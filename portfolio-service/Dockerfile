FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /app/portfolio-service-bin .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/portfolio-service-bin .
EXPOSE 50051
CMD ["./portfolio-service-bin"]
