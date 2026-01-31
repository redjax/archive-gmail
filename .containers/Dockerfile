ARG GO_CONTAINER_IMG=golang:1.25-alpine
ARG RUNTIME_IMG=alpine:latest

FROM ${GO_CONTAINER_IMG} AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /archive-gmail ./cmd/archive-gmail

FROM ${RUNTIME_IMG}

RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /archive-gmail /app/archive-gmail
COPY .env* ./

ENTRYPOINT ["/app/archive-gmail"]
