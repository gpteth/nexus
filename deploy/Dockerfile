# ---- 构建阶段 ----
FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 纯 Go 代码，CGO_ENABLED=0 产出静态二进制，便于 distroless 运行。
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/nexus ./cmd/nexus

# ---- 运行阶段 ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/nexus /usr/local/bin/nexus
COPY --from=builder /src/configs/config.example.yaml /etc/nexus/config.yaml

EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/nexus", "-config", "/etc/nexus/config.yaml"]
