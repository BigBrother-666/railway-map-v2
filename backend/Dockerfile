FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/bcts-server ./cmd/server

FROM alpine:3.20

WORKDIR /app
COPY --from=build /out/bcts-server /app/bcts-server
COPY config.yml /app/config.yml
RUN mkdir -p /app/data

EXPOSE 8080
CMD ["/app/bcts-server", "-config", "/app/config.yml", "-env", "/app/.env"]
