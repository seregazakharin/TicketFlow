FROM golang:1.20-alpine AS build

ARG SERVICE
WORKDIR /app

RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN test -n "$SERVICE" && CGO_ENABLED=0 GOOS=linux go build -o /out/app ./cmd/${SERVICE}

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
