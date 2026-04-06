FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /neo4g ./cmd/neo4g
RUN CGO_ENABLED=0 go build -o /neo4g-gateway ./cmd/neo4g-gateway

FROM alpine:3.21 AS neo4g
RUN apk add --no-cache ca-certificates
COPY --from=builder /neo4g /usr/local/bin/neo4g
EXPOSE 7474
ENTRYPOINT ["neo4g"]

FROM alpine:3.21 AS gateway
RUN apk add --no-cache ca-certificates
COPY --from=builder /neo4g-gateway /usr/local/bin/neo4g-gateway
EXPOSE 7480
ENTRYPOINT ["neo4g-gateway"]
