# FPKM 官方镜像
#
# 用途：在容器里运行 FPKM，安装并运行 .fpk 应用包。
#
# 构建（需先产出 dist/fpkm-linux-<arch>）：
#   docker build -t qilinzhu/fpkm:1.0.1 .
#
# 运行：
#   docker run -d --name fpkm -p 7666:7666 \
#     -v /path/to/fpkm-data:/app/data qilinzhu/fpkm:latest
#
# 说明：
#   - FPKM 本身是静态二进制，但 .fpk 应用各式各样（bash 脚本、
#     Node.js、Python、原生二进制…），因此基础镜像选用 debian-slim
#     并预装常见运行时，否则多数应用装得上却跑不起来。
#   - 数据目录固定为 /app/data（与二进制同级的 data/），挂载卷即可持久化。

FROM debian:bookworm-slim

# 构建参数：目标架构（amd64 / arm64），由 buildx 传入
ARG TARGETARCH=amd64
ARG FPKM_VERSION=dev

LABEL org.opencontainers.image.title="FPKM" \
      org.opencontainers.image.description="Run .fpk (fnOS) offline packages on any Linux" \
      org.opencontainers.image.url="https://github.com/FNOSP/FPKM" \
      org.opencontainers.image.source="https://github.com/FNOSP/FPKM" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${FPKM_VERSION}"

# 应用运行所需的基础依赖：
#   bash/coreutils/procps —— .fpk 的 cmd/main 脚本与进程管理
#   curl/wget              —— 应用下载资源
#   ca-certificates        —— HTTPS
#   tzdata                 —— 正确的时区（应用与日志都依赖）
#   iproute2/net-tools     —— 部分应用启动脚本会调用 ss/netstat 探测端口
#   tini                   —— 作为 PID 1 回收僵尸进程，正确处理信号
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      bash \
      ca-certificates \
      coreutils \
      curl \
      wget \
      procps \
      psmisc \
      iproute2 \
      net-tools \
      tzdata \
      tini \
      tar \
      gzip \
 && rm -rf /var/lib/apt/lists/*

# 时区可通过环境变量覆盖
ENV TZ=Asia/Shanghai

WORKDIR /app

# 复制对应架构的二进制（宿主机需先执行 ./build.sh release）
COPY dist/fpkm-linux-${TARGETARCH} /app/fpkm
RUN chmod +x /app/fpkm

# 数据目录：apps / logs / password.json 等都在这里
# 声明为 VOLUME，用户不挂载时也能工作（写入容器层）
RUN mkdir -p /app/data
VOLUME ["/app/data"]

EXPOSE 7666

# 健康检查：能取到首页即视为正常
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS http://127.0.0.1:7666/ >/dev/null || exit 1

# 用 tini 做 PID 1：FPKM 会拉起应用子进程，需要正确的信号转发与回收
ENTRYPOINT ["/usr/bin/tini", "--"]

# 容器内必须监听 0.0.0.0，否则宿主机无法访问
CMD ["/app/fpkm", "web", "--bind", "0.0.0.0", "--port", "7666"]
