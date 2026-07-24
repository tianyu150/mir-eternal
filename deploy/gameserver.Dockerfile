FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gameserver ./cmd/gameserver

FROM alpine:3.22
RUN addgroup -S mir && adduser -S -G mir mir && mkdir -p /var/lib/mir/game && chown -R mir:mir /var/lib/mir
COPY --from=build /out/gameserver /usr/local/bin/gameserver
COPY deploy/gameserver.json /etc/mir/gameserver.json
USER mir
EXPOSE 8701/tcp 6678/udp
ENTRYPOINT ["gameserver", "-config", "/etc/mir/gameserver.json"]
