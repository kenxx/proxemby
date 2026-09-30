# syntax=docker/dockerfile:1

FROM rust:1-alpine AS build

RUN apk add --no-cache build-base

WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY .cargo .cargo
# Build dependencies first so they are cached separately from the source.
RUN mkdir src && echo 'fn main() {}' > src/main.rs && touch src/lib.rs \
    && cargo build --release --locked \
    && rm -rf src

COPY src src
RUN touch src/main.rs src/lib.rs && cargo build --release --locked

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /src/target/release/proxemby /usr/bin/proxemby
COPY examples/proxemby.toml /etc/proxemby/proxemby.toml

EXPOSE 8080

ENTRYPOINT ["/usr/bin/proxemby"]
CMD ["--config", "/etc/proxemby/proxemby.toml"]
