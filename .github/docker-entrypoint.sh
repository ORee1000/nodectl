#!/bin/sh
set -eu

umask 077
data_dir=/app/data

if [ "$(id -u)" -eq 0 ]; then
    mkdir -p "$data_dir"

    # 兼容旧镜像以 root 写入的数据库、证书和下载文件。
    chown -R nodectl:nodectl "$data_dir"
    exec su-exec nodectl:nodectl "$@"
fi

if [ ! -d "$data_dir" ] || [ ! -w "$data_dir" ]; then
    echo "错误：$data_dir 不存在或当前用户无写入权限" >&2
    exit 1
fi

exec "$@"
