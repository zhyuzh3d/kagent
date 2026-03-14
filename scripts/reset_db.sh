#!/usr/bin/env bash
# scripts/reset_db.sh

# 切换到项目根目录
cd "$(dirname "$0")/.."
ROOT_DIR=$(pwd)
DB_PATH="data/kagent.db"

# 获取操作范围参数，默认为 messages
# messages: 只清空消息记录表 (保留用户、项目、会话线等元数据)
# log: 清空所有用户运行过程日志 (*.jsonl)
# data: 清空所有用户 Surface 业务持久化数据
# all: 以上全部清理
SCOPE=${1:-messages}

echo "=> [Kagent Reset] 准备执行清理操作..."
echo "=> 目标范围: $SCOPE"

# 1. 尝试停止服务器（如果正在运行），避免 SQLite 文件锁冲突
echo "=> 正在尝试停止服务器 (POST /admin/shutdown) ..."
curl -s -X POST http://127.0.0.1:18080/admin/shutdown > /dev/null 2>&1
# 稍微等待确保进程安全退出
sleep 2

# 确认 sqlite3 命令是否存在
if ! command -v sqlite3 &> /dev/null; then
    echo "错误: 系统未安装 sqlite3，无法清理数据库表。"
    exit 1
fi

# A. 清理数据库逻辑
clear_messages() {
    echo "=> [DB] 正在清理 messages 数据表..."
    if [ -f "$DB_PATH" ]; then
        # 严格执行: 只清空 messages 表，保留 users/projects 等
        sqlite3 "$DB_PATH" "DELETE FROM messages;"
        # 释放碎片空间缩小文件体积
        sqlite3 "$DB_PATH" "VACUUM;"
        echo "   [DONE] messages 数据表已清空。"
    else
        echo "   [SKIP] 未找到数据库文件 $DB_PATH。"
    fi
}

# B. 清理日志逻辑
clear_log() {
    echo "=> [FILE] 正在物理删除所有 .jsonl 日志文件..."
    # 递归查找 data/users 下的所有 jsonl 并删除，不会触及 custom_config
    find data/users -name "*.jsonl" -type f -delete 2>/dev/null
    echo "   [DONE] 所有 ops 日志 (.jsonl) 已清理。"
}

# C. 清理业务数据逻辑
clear_data() {
    echo "=> [FILE] 正在物理删除所有 Surface 业务数据..."
    # 清空每个用户目录下的 surface_data 子目录内容，但保留 data/users 结构
    if [ -d "data/users" ]; then
        rm -rf data/users/*/surface_data/*
        echo "   [DONE] 所有 surface_data 业务文件已清理。"
    else
        echo "   [SKIP] 未找到用户数据目录。"
    fi
}

# 根据参数决定执行动作
case "$SCOPE" in
    messages)
        clear_messages
        ;;
    log)
        clear_log
        ;;
    data)
        clear_data
        ;;
    all)
        clear_messages
        clear_log
        clear_data
        ;;
    *)
        echo "错误: 未知的范围参数 '$SCOPE'"
        echo "用法: $0 [messages|log|data|all]"
        exit 1
        ;;
esac

echo "=> 操作成功完成！"
echo "=> 安全提示: .jwt_secret 和 user_custom_config.json 已被安全保护，未受影响。"

