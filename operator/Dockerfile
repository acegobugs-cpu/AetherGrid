# Build the AETHER-GRID operator binary.
FROM golang:1.26 AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /operator ./cmd/operator

# Minimal runtime image, runnable as a non-root user.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /operator /operator
USER 65532:65532
ENTRYPOINT ["/operator"]