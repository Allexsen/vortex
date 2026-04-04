FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o /app ./cmd/search

FROM alpine:3.21

RUN addgroup -S vortex && adduser -S -G vortex vortex

WORKDIR /home/vortex

COPY --from=build /app /app
COPY --from=build /src/cmd/search/static ./cmd/search/static

RUN mkdir logs && chown vortex:vortex logs

USER vortex

ENTRYPOINT ["/app"]
