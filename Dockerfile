FROM golang:1.25-alpine AS build

RUN apk add --no-cache ca-certificates git
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/finli-live .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=build /out/finli-live /usr/local/bin/finli-live

EXPOSE 9876

CMD ["finli-live"]
