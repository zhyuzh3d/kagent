#!/usr/bin/env bash
# scripts/reset_db.sh

# 切换到项目根目录
cd "$(dirname "$0")/.."

echo "=> 正在尝试停止 Kagent 服务器..."
curl -s -X POST http://127.0.0.1:18080/admin/shutdown > /dev/null
# 稍微等待确保进程已经安全释放文件锁退出
sleep 2

echo "=> 正在清理 SQLite 数据库及日志文件..."
rm -f data/kagent.db
rm -f data/kagent.db-shm
rm -f data/kagent.db-wal

echo "=> 数据库已成功清空！"
echo "下一次启动应用时，程序（sqlite_store.go 的 init 方法）会自动重新创建数据库及初始化默认表结构记录。"
