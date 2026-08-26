FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/game-server ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.22
RUN addgroup -S texas && adduser -S -G texas texas
COPY --from=build /out/game-server /usr/local/bin/game-server
COPY --from=build /out/migrate /usr/local/bin/migrate
USER texas
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/game-server"]
