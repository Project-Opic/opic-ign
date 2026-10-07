# opic-techbase 后端镜像(gopherforge 通用 Dockerfile 模式:多阶段构建)
FROM golang:1.27-alpine AS builder
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/techbase ./cmd/techbase

# 运行位镜像：ubuntu 24.04 而非 alpine —— 裁定 Q-68 下本容器自身即执行机，命令面是真 kubeasz
# （ezctl 是 bash 脚本 + ansible-playbook），alpine 无 bash/ansible 会让每个任务判 LOST。
# 装整包 ansible（含 collections）而非裸 ansible-core：kubeasz 的 os-harden/prepare 用裸模块名
# 调 pam_limits / sysctl / mount / acls 等（属 ansible.posix、community.general），
# 只有 core 会在 01 步即 "couldn't resolve module/action 'pam_limits'" 退出码 1。
# 版本与 3.200 宿主 P1.5 实测同源（ansible 9.2.0 + core 2.16.3），避免控制端差异引入新变量。
FROM ubuntu:24.04
ENV TZ=Asia/Shanghai DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        ansible openssh-client python3 bash wget ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd -g 10001 app && useradd -m -u 10001 -g 10001 -s /usr/sbin/nologin app
# kubeasz 的 01.prepare 末尾有一条控制机本地任务：把 ezctl/ezdown 软链到 /usr/bin（无 force）。
# 执行位进程是非 root 的 app，建链会 EACCES 直接判失败；这里在镜像里预先把链建好，
# 该任务命中"已存在且指向一致"即 ok/changed=false。软链目标 /etc/kubeasz 是运行期挂载，
# 构建期允许悬空。
RUN ln -s /etc/kubeasz/ezctl /usr/bin/ezctl && ln -s /etc/kubeasz/ezdown /usr/bin/ezdown
WORKDIR /app
COPY --from=builder /out/techbase ./techbase
COPY configs ./configs
COPY models ./models
COPY migrations ./migrations
USER app
EXPOSE 9680
ENTRYPOINT ["/app/techbase"]
