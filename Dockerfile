FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG SERVICE
RUN go build -o /app ./cmd/${SERVICE}

FROM alpine:3.21

RUN addgroup -S vortex && adduser -S -G vortex vortex

WORKDIR /home/vortex

COPY --from=build /app /app

RUN mkdir logs && chown vortex:vortex logs

USER vortex

ENTRYPOINT ["/app"]
