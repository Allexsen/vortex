FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o /app ./cmd/search

FROM alpine:3.21

WORKDIR /root

COPY --from=build /app /app
COPY --from=build /src/cmd/search/static ./cmd/search/static

ENTRYPOINT ["/app"]
