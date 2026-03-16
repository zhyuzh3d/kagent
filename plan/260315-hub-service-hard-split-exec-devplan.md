# Hub / Service 彻底硬拆分执行计划

- 日期：2026-03-15
- 类型：开发计划（执行版）
- 目标：将当前仓库重构为 `Hub + 独立 Service 子项目`，并完成运行链路验证与冗余清理。

## 1. 目标定义（本次必须达成）
1. Hub 与 Service 代码目录彻底分离：
   - `hub/cmd/hub`
   - `hub/internal/*`
   - `services/chat-server/cmd/chat-server`
   - `services/chat-server/internal/*`
   - `services/ai-doubao/cmd/ai-doubao`
   - `services/ai-doubao/internal/*`
2. 每个服务运行时依赖代码仅来自本服务 `internal`。
3. 旧单体路径停止作为运行入口，不再承担主链路。
4. chat 主链路可用：Auth -> Project/Thread API -> WS。
5. 完成数据库全量重置与旧运行产物清理。

## 2. 迁移策略
1. 先复制再切换：先复制运行时依赖代码到目标目录，再切换入口 import。
2. 入口切换：
   - Hub 构建目标切换到 `./hub/cmd/hub`
   - chat-server 构建目标切换到 `./services/chat-server/cmd/chat-server`
   - ai-doubao 构建目标切换到 `./services/ai-doubao/cmd/ai-doubao`
3. 脚本先行：`deploy.sh` / `reset_db.sh` 先适配新入口，再做大清理。
4. 清理顺序：仅在部署与 smoke 全通过后删除旧入口与冗余文件。

## 3. 分阶段执行
### Phase A：目录与入口硬拆分
- 新建 `hub/` 与 `services/*` 子项目入口目录。
- 切换 import 到各自 `internal`。

### Phase B：运行链路修复
- 修复编译错误。
- 保证 deploy 可一键拉起 3 服务。

### Phase C：chat 功能回归
- API：`/api/auth/*`、`/api/projects*`、`/api/threads*`
- WS：`/ws` 建连与基础消息往返

### Phase D：彻底清理
- 删除旧入口文件与无引用冗余文件。
- 执行全量 reset，清空历史 DB 内容。

## 4. 验收标准
1. `go test ./...` 通过。
2. `scripts/deploy.sh` 成功拉起 hub/chat-server/ai-doubao。
3. smoke：注册、登录态、项目线程 CRUD、WS 建连成功。
4. `scripts/reset_db.sh` 后 `data/` 无旧库文件。
5. 旧入口和冗余脚本不再存在。

## 5. 风险与控制
1. 风险：拆分后 import 断裂。
   - 控制：阶段性编译与回归，逐步清理。
2. 风险：启动顺序导致 chat-server 健康失败。
   - 控制：deploy 固定先 ai-doubao 后 chat-server。
3. 风险：清理过度误删运行必要文件。
   - 控制：以 smoke+test 结果作为删除门禁。
