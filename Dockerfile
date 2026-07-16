FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /trollssh ./cmd/trollssh

FROM alpine:3.22
WORKDIR /home/app

RUN apk add --no-cache ffmpeg

COPY --from=build /trollssh /usr/local/bin/trollssh

CMD ["trollssh"]
