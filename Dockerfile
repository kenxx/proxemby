# syntax=docker/dockerfile:1

FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/proxemby ./cmd/proxemby

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/proxemby /usr/bin/proxemby
COPY examples/proxemby.toml /etc/proxemby/proxemby.toml

EXPOSE 8080

ENTRYPOINT ["/usr/bin/proxemby"]
CMD ["--config", "/etc/proxemby/proxemby.toml"]
