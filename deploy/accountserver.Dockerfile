FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/accountserver ./cmd/accountserver

FROM alpine:3.22
RUN addgroup -S mir && adduser -S -G mir mir && mkdir -p /var/lib/mir/accounts && chown -R mir:mir /var/lib/mir
COPY --from=build /out/accountserver /usr/local/bin/accountserver
COPY deploy/accountserver.json /etc/mir/accountserver.json
USER mir
EXPOSE 7000/udp
ENTRYPOINT ["accountserver", "-config", "/etc/mir/accountserver.json"]
