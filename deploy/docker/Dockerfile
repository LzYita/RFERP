# RFERP API 服务镜像（仅 cmd/server，无 Fyne/CGO）
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rferp-server ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
	&& rm -rf /var/lib/apt/lists/*
COPY --from=build /out/rferp-server /usr/local/bin/rferp-server
# 运行数据目录（备份/导出/日志）挂卷到 /data
ENV MES_DATA_DIR=/data
EXPOSE 8080
ENTRYPOINT ["rferp-server"]
CMD ["-addr", ":8080"]
