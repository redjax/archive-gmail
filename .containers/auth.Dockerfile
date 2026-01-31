ARG GO_CONTAINER_IMG=golang:1.25-alpine
FROM ${GO_CONTAINER_IMG} AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /authenticate ./cmd/authenticate

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /authenticate /app/authenticate

VOLUME ["/token"]

ENTRYPOINT ["/app/authenticate"]
