FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /keystone ./cmd/keystone

FROM alpine:3.20
RUN adduser -D -u 10001 keystone
COPY --from=build /keystone /usr/local/bin/keystone
USER keystone
EXPOSE 8080
ENTRYPOINT ["keystone"]
