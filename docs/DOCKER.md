# Docker 部署

FPKM 提供官方镜像，可在容器中安装并运行 `.fpk` 应用包。

镜像地址：**`qilinzhu/fpkm`**

| 标签 | 说明 |
|------|------|
| `latest` | 最新稳定版 |
| `1.0.1` | 指定版本 |

支持架构：`linux/amd64`、`linux/arm64`。

## 快速开始

```bash
docker run -d \
  --name fpkm \
  -p 7666:7666 \
  -v /path/to/fpkm-data:/app/data \
  --restart unless-stopped \
  qilinzhu/fpkm:latest
```

浏览器打开 `http://<宿主机IP>:7666`，首次访问设置管理密码。

## docker compose

```yaml
services:
  fpkm:
    image: qilinzhu/fpkm:latest
    container_name: fpkm
    ports:
      - "7666:7666"
    volumes:
      # 必须持久化，否则重建容器会丢失已安装应用与密码
      - ./fpkm-data:/app/data
    restart: unless-stopped
    # 部分应用需要访问宿主机网络能力（如端口探测、反向代理）
    # network_mode: host
```

## 数据持久化

FPKM 的所有数据都在可执行文件旁的 `data/` 目录，容器内即 `/app/data`：

```
/app/data/
├── apps/{appname}/    已安装应用（含程序、配置、数据）
├── bin/
├── logs/
├── password.json      管理密码
├── sources.json       应用源
├── autostart.json     自动启动的应用
└── access-overrides.json   接入方式设置
```

**务必挂载这个目录**，否则 `docker rm` 后数据全部丢失。

## 关于基础镜像

镜像基于 `debian:bookworm-slim`，并预装了 `.fpk` 应用常用的运行时依赖：

| 组件 | 用途 |
|------|------|
| `bash` / `coreutils` / `procps` | `.fpk` 的 `cmd/main` 脚本与进程管理 |
| `curl` / `wget` / `ca-certificates` | 应用下载资源、HTTPS |
| `iproute2` / `net-tools` | 部分应用启动脚本用 `ss`/`netstat` 探测端口 |
| `tzdata` | 正确时区（可用 `-e TZ=` 覆盖） |
| `tini` | 作为 PID 1，正确转发信号并回收应用子进程 |

**为什么不用 alpine**：`alpine` 使用 musl 且缺少 glibc 兼容层，而大量 `.fpk` 应用是
针对 glibc 编译的 ELF，在 alpine 里会直接报 `not found`（实际是动态链接器缺失）。
`debian-slim` 兼容性明显更好。

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `TZ` | `Asia/Shanghai` | 时区 |

## 常用操作

```bash
# 查看日志
docker logs -f fpkm

# 进入容器（排查应用问题）
docker exec -it fpkm bash

# 忘记密码：清除密码记录
docker exec fpkm /app/fpkm web --reset-password

# 升级镜像（数据保留在卷里，不会丢）
docker pull qilinzhu/fpkm:latest
docker rm -f fpkm
# 重新执行 docker run
```

## 网络模式说明

默认使用 bridge 网络（端口映射）。部分应用需要：

- 探测宿主机端口 → 建议 `--network host`
- 让局域网直接访问应用端口 → 建议 `--network host`
- 被其他容器访问 → 加入同一自定义网络

使用 host 网络时可去掉 `-p`：

```bash
docker run -d --name fpkm --network host \
  -v /path/to/fpkm-data:/app/data \
  qilinzhu/fpkm:latest
```

## 已知限制

- **Docker 类应用**（`.fpk` 里声明需要 Docker 的）在容器内无法直接运行，
  需要挂载宿主机的 Docker socket（`-v /var/run/docker.sock:/var/run/docker.sock`），
  或此类应用建议直接在宿主机部署 FPKM。
- 部分应用会调用 fnOS 私有服务或校验飞牛网关签名，容器环境同样无法满足；
  可在「应用设置」里改为**端口直连**方式使用。
- 容器内以 root 运行，`.` 与宿主机的权限差异可能导致个别应用行为不同。

## 自行构建镜像

```bash
# 1. 先构建两个架构的二进制
VERSION=1.0.1 ./build.sh release

# 2. 构建镜像（需要 buildx 支持多架构）
docker buildx create --use --name multiarch   # 首次需要
./scripts/build-image.sh --push --user=qilinzhu
```
