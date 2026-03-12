# 数据库 Schema 与 ID 生成升级计划

## 1. 现状问题
目前 `users`, `projects`, `threads` 表的数据对象，分别写死了默认的 `default`, `project-default`, `chat-default` 作为主键 ID。这样的设计导致前端允许重命名项目名称 / 会话名称时，会直接撞车，甚至导致数据混淆崩溃。
同时，用户提出数据库里少了“农历（lunar_date）”和“星期（day_of_week）”的字段支持，需要连带重置一并解决。

## 2. 升级改造点

### 2.1 全局 ID 唯一化改造
弃用旧的 `xxx-default` 命名作为主键（ID），转而使用 `crypto/rand` 的随机 UUID/Hex 来作为底层唯一键（保证与表面名称解耦，名称只是一个 `title`，让用户随便改）。
- **`sqlite_store.go`的 `init()` 改造**：在首次建表后，`SELECT` 判断系统里是否已经有常驻 `user_id / project_id / thread_id`，如果没有，则即时生成 `usr-xxxxx` / `prj-xxxxx` / `thd-xxxxx`，然后 `INSERT` 进去。下次启动直接读取，保证不管单机怎么重启，ID还是那个稳定的全局唯一ID。
- 修改 `internal/session.go` 中的硬编码 `storeUserID = "default"`，改为读取 `sqliteStore.userID` 等，确保所有的消息存储均绑定到由 DB 生成的最新动态 ID。

### 2.2 Schema 增加农历与星期
经排查，目前 `messages` 已经预留了 `created_at_local_weekday`, `created_at_local_lunar`。本次会将日历增强辐射到 `threads`、`projects` 以及后续相关的元数据建立语句中。对于基础操作，增加 `lunar_date` 与 `day_of_week`。
- 新增轻量级 Go time 的 `Weekday()` 转换 `"星期X"`。
- 新增简单的农历空打底（或者直接引入简单的农历字符串计算），若太复杂，目前按用户诉求至少先保证把字段加好，留白或用预留词占坑，日后再从前端传或者集成第三方日历库。

### 2.3 无痛重置库 (Wipe & Reset)
在执行应用编译启动前，强制删除原有的 `data/kagent.db`。从而在新启动时自动触发新的 Schema 和动态 Hex ID 生成。

## 3. 约束
1. 不能碰原来的消息内容解析逻辑；
2. ID前缀建议保留 `usr-`, `prj-`, `thd-` 便于肉眼排错。
