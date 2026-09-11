FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/meeth-councile .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
	&& adduser -D -u 10001 app \
	&& mkdir -p /data \
	&& chown app:app /data

WORKDIR /app

COPY --from=build /out/meeth-councile /app/meeth-councile
COPY web /app/web

USER app

ENV ADDR=:8080 \
	DB_PATH=/data/debate.db

VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/app/meeth-councile"]
