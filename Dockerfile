FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/collector ./cmd/collector

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates && addgroup -S nodewatch && adduser -S -G nodewatch nodewatch
COPY --from=build /out/collector /usr/local/bin/collector
USER nodewatch
WORKDIR /nonexistent
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=6s --start-period=30s --retries=3 CMD ["collector", "healthcheck"]
ENTRYPOINT ["collector"]
