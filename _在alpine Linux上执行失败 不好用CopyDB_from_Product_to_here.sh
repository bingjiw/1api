#!/bin/sh

#更新包管理器：
apk update

#安装rsync：
apk add rsync

#验证安装：确保rsync命令可用。
rsync --version

#安装OpenSSH：
apk add openssh


# 远程服务器的IP地址或主机名
REMOTE_HOST="188.166.182.236"
# 远程服务器的用户名
REMOTE_USER="root"
# 远程文件的路径
REMOTE_FILE="/data/one-api.db"

# 使用rsync命令从远程服务器获取文件并保存到当前目录
rsync -avz "$REMOTE_USER@$REMOTE_HOST:$REMOTE_FILE" .
