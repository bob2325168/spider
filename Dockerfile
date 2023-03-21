FROM golang:1.18-alpine as builder
LABEL maintainer="303433129@qq.com"
WORKDIR /app
COPY . /app
RUN go mod download
RUN go build -o crawler main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/crawler ./
COPY --from=builder /app/config.toml ./
CMD ["./crawler","worker"]