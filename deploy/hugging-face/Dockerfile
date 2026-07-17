FROM golang:1.25-alpine3.21 AS builder

RUN apk add --no-cache git

WORKDIR /src

RUN git clone --depth 1 --branch main \
    https://github.com/RezaHeshmati30/TG-FileStreamBot.git .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -o /app/fsb \
    -ldflags="-w -s" \
    ./cmd/fsb

FROM scratch

COPY --from=builder /app/fsb /app/fsb

EXPOSE 8080

ENTRYPOINT ["/app/fsb", "run"]
