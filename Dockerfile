FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG SERVICE
RUN go build -o /app ./cmd/${SERVICE}

FROM alpine:3.21

WORKDIR /root

COPY --from=build /app /app

ENTRYPOINT ["/app"]
