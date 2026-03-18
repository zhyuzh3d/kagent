# AI & Chat 服务标准化重构开发计划 (260316)

- **指导文档**: `plan/260316-kagent-architecture-philosophy-ana.md`
- **关联计划**: `plan/260316-hub-identity-logging-devplan.md`
- **日期**: 2026-03-16
- **状态**: 开发中

---

## 1. 核心目标

本计划旨在解决 `ai-doubao` 与 `chat-server` 之间存在的 **私有后端直连** 问题，将其重构为符合 Hub 架构规范的"能力层-应用层"标准协作模式。

1. **架构合规**：废弃 `chat-server → ai-doubao` 的直连绕路（当前 `ServiceProviderFactory` 直拨 `http://127.0.0.1:18081/v1/*`），强制所有跨服务调用走 `Service → Hub → Service` 路径。
2. **能力原子化**：`ai-doubao` 转型为纯粹的 AI 原子能力提供者（Atomic Provider），剥离所有业务逻辑。
3. **应用标准化**：`chat-server` 转型为标准的业务协调者（Coordinator），不再自行管理 `ai-doubao` 的进程生命周期和健康检查。
4. **清理冗余**：彻底删除 `ai-doubao` 模块中因前期硬拆分留下的约 70% 冗余模板代码。
5. **元数据修复**：修复 `Streaming` 字段注册 Bug，使 Hub 路由引擎能正确识别流式工具。

---

## 2. 现状问题分析（代码级确认）

### 2.1 架构违规：chat-server 直连 ai-doubao

| 问题 | 文件 | 关键行 | 现象 |
|------|------|--------|------|
| 直连 ASR | `chat-server/.../service_provider_factory.go` L104 | `buildServiceWSURL(c.baseURL, "/v1/asr/stream")` — 直拨 ai-doubao WS |
| 直连 LLM | `chat-server/.../service_provider_factory.go` L285 | `http.MethodPost, baseURL+"/v1/llm/stream"` — 直拨 ai-doubao SSE |
| 直连 TTS | `chat-server/.../service_provider_factory.go` L409 | `http.MethodPost, baseURL+"/v1/tts/synthesize"` — 直拨 ai-doubao HTTP |
| 进程管理越界 | `chat-server/.../ai_service_manager.go` L67-76 | `chat-server` 直接管理 ai-doubao 进程启动/健康检查/重启/关停 |
| 硬编码 ServiceID | `chat-server/.../service_provider_factory.go` L254,385,476 | `ServiceID: "ai-doubao"` 写死在审计记录中 |

### 2.2 ai-doubao 缺失标准端点

| 问题 | 文件 | 现象 |
|------|------|------|
| 无 `/service/tool/exec` | `ai-doubao/cmd/ai-doubao/main.go` | 只暴露私有端点 `/v1/asr/stream`、`/v1/llm/stream`、`/v1/tts/synthesize`，不响应 Hub 标准工具调用 |
| 无 Token 校验 | `ai-doubao/cmd/ai-doubao/main.go` | 所有端点均无 `X-Hub-Service-Token` 校验，任何客户端均可直连 |

### 2.3 Streaming 注册 Bug

| 问题 | 文件 | 行 | 现象 |
|------|------|-----|------|
| 布尔判断写死 | `ai-doubao/cmd/ai-doubao/main.go` L414 | `strings.EqualFold(descriptor.Streaming, "stream")` — 但实际注册值为 `ws_binary`/`sse`/`none`，导致 **所有工具的 `Streaming` 都被注册为 `false`** |
| 相同 Bug (chat-server) | `chat-server/cmd/chat-server/main.go` L570 | `strings.EqualFold(descriptor.Streaming, "stream")` — 同样的判断错误 |
| Hub 端丢失 | `hub/cmd/hub/main.go` L998-999 | Hub 反向转换 `if t.Streaming { streaming = "stream" }` 也与服务端原值不匹配 |

### 2.4 Hub WS 代理硬编码

| 问题 | 文件 | 行 | 现象 |
|------|------|-----|------|
| 硬编码 chat-server | `hub/cmd/hub/main.go` L275 | `hubPlatform.GetService("chat-server")` — `/api/tool/ws` 写死指向 chat-server |
| 无 service_id 路由 | `hub/cmd/hub/main.go` L264-297 | 不支持根据 query 参数选择目标服务 |

### 2.5 ai-doubao 冗余代码

以下文件是从 chat-server 硬拷贝过来的模板代码，`ai-doubao` 作为原子能力提供者完全不需要：

| 文件 | 行数 | 说明 |
|------|------|------|
| `session.go` | 1795 | 完整的对话会话管理，ai-doubao 无需 |
| `sqlite_store.go` | ~1000 | 数据库存储层，ai-doubao 无需 |
| `pipeline.go` | 619 | LLM→TTS 管道编排，ai-doubao 无需 |
| `hub_platform.go` | 920 | Hub 平台注册/Token 逻辑，ai-doubao 无需用完整版 |
| `hub_builtins.go` | ~200 | 内置工具定义，ai-doubao 无需 |
| `auth.go` | ~100 | 认证逻辑，ai-doubao 无需 |
| `operation_log.go` | ~100 | 操作日志，ai-doubao 无需 |
| `provider_factory.go` | 40 | ProviderFactory 接口，ai-doubao 无需 |
| `runtime_root.go` | ~50 | 运行时根路径，ai-doubao 无需 |
| **合计** | **~4824** | **占 ai-doubao internal/app 代码量约 70%** |

---

## 3. 模块职责重新定义

| 模块 | 旧职责 | 新职责 (Refactored) |
| :--- | :--- | :--- |
| **ai-doubao** | 私有 AI 后端，包含 Session/Pipeline/SQLite/Auth 等冗余模板代码 | **原子能力层 (Atomic Provider)**：仅提供 ASR (WS)、LLM (SSE)、TTS (Exec) 三个原子工具。严格遵循 Hub Tool 协议，通过 `/service/tool/exec` 响应标准调用，通过 `X-Hub-Service-Token` 校验来源。 |
| **chat-server** | 业务中心 + 内嵌 ai-doubao 进程管理 + 直连 AI 后端 | **业务协调者 (Coordinator)**：管理会话、历史记录、业务调度。**不再**自行管理 ai-doubao 进程。Hub 负责（已有能力）。 |
| **Hub** | 基础认证、静态工具转发、硬编码 WS 代理指向 chat-server | **中枢网关 (Mediator)**：支持通用流式工具代理（WS 可按 query 参数动态路由），进行安全审计和 Token 注入。 |

---

## 4. 具体改造步骤

### Phase 1: ai-doubao — Streaming 注册 Bug 修复 + 元数据补齐

> [!IMPORTANT]
> 这是最小改动、最高优先级的修复，修复后 Hub 路由引擎才能正确识别流式工具。

#### [MODIFY] `services/ai-doubao/cmd/ai-doubao/main.go`

**Bug 修复**：`toSupervisorToolsFromDescriptors` 函数 (L404-418)

```diff
 func toSupervisorToolsFromDescriptors(version string, descriptors []app.AIServiceToolDescriptor) []toolproto.ServiceTool {
     tools := make([]toolproto.ServiceTool, 0, len(descriptors))
     for _, descriptor := range descriptors {
         toolID := strings.TrimSpace(descriptor.Name)
         if toolID == "" {
             continue
         }
+        streaming := strings.TrimSpace(descriptor.Streaming)
+        isStreaming := streaming != "" && !strings.EqualFold(streaming, "none")
         tools = append(tools, toolproto.ServiceTool{
             ToolID:    toolID,
             Version:   strings.TrimSpace(version),
-            Streaming: strings.EqualFold(strings.TrimSpace(descriptor.Streaming), "stream"),
+            Streaming:            isStreaming,
+            TimeoutMS:            descriptor.TimeoutMSDefault,
+            CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
         })
     }
     return tools
 }
```

#### [MODIFY] `services/chat-server/cmd/chat-server/main.go`

**同步修复**：`toSupervisorTools` 函数 (L560-576)

```diff
 func toSupervisorTools(manifest app.ServiceManifest) []toolproto.ServiceTool {
     tools := make([]toolproto.ServiceTool, 0, len(manifest.Provides))
     for _, descriptor := range manifest.Provides {
         toolID := strings.TrimSpace(descriptor.ToolID)
         if toolID == "" {
             continue
         }
+        streaming := strings.TrimSpace(descriptor.Streaming)
+        isStreaming := streaming != "" && !strings.EqualFold(streaming, "none")
         tools = append(tools, toolproto.ServiceTool{
             ToolID:               toolID,
             Version:              strings.TrimSpace(manifest.Version),
-            Streaming:            strings.EqualFold(strings.TrimSpace(descriptor.Streaming), "stream"),
+            Streaming:            isStreaming,
             TimeoutMS:            descriptor.TimeoutMSDefault,
             CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
         })
     }
     return tools
 }
```

---

### Phase 2: ai-doubao — 新增标准 `/service/tool/exec` 端点

> [!IMPORTANT]
> 此端点是 Hub 工具网关向 ai-doubao 发起调用的标准入口。当前 ai-doubao 完全没有此端点。

#### [MODIFY] `services/ai-doubao/cmd/ai-doubao/main.go`

新增 `/service/tool/exec` handler，需要：

1. **加载 service secret**：从 `data/.service_secret` 加载共享密钥
2. **校验 `X-Hub-Service-Token`**：使用 `pkg/hubsvc.VerifyServiceSessionTokenLoose` 校验
3. **解析 `toolproto.CallRequest`**：标准工具调用协议
4. **分发到内部能力**：
   - `ai.speech.tts` → 调用现有 `tts.Synthesize()` 并返回 `CallResponse`
   - `ai.llm.stream` → 返回错误码提示使用 SSE 端点（或直接支持同步 LLM 调用）

```go
mux.HandleFunc("/service/tool/exec", func(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        writeToolResponse(w, http.StatusMethodNotAllowed, toolproto.CallResponse{...})
        return
    }
    // 1. 校验 X-Hub-Service-Token
    tokenClaims, err := hubsvc.VerifyServiceSessionTokenLoose(
        r.Header.Get("X-Hub-Service-Token"), serviceSecret)
    if err != nil || tokenClaims.ServiceID != "ai-doubao" {
        writeToolResponse(w, http.StatusUnauthorized, ...)
        return
    }
    // 2. 解析 CallRequest
    var req toolproto.CallRequest
    json.NewDecoder(r.Body).Decode(&req)
    // 3. 按 tool_id 分发
    switch req.ToolID {
    case "ai.speech.tts":
        // 调用 tts.Synthesize，返回 audio_base64
    case "ai.llm.stream":
        // 返回提示："use SSE endpoint /v1/llm/stream for streaming"
        // 或实现同步调用
    default:
        // tool_not_found
    }
})
```

> 保留现有 `/v1/*` 端点不删除，因为 chat-server 的 WS 代理流（Hub `/api/tool/ws` → chat-server `/service/tool/ws`）中 ASR 仍然通过 chat-server 内部的 `ServiceASRClient` 直连。这部分的 Hub 中介化改造属于 Phase 4 的范围。

---

### Phase 3: chat-server — 移除直连依赖

> [!IMPORTANT]
> 这是本次重构的核心变更。移除 chat-server 对 ai-doubao 的直接进程管理和客户端直连。

#### [MODIFY] `services/chat-server/cmd/chat-server/main.go`

**移除内容** (约 30 行)：

```diff
-   aiServiceCfg := cfg.EffectiveAIService()            // L68
-   if !app.IsServiceMode(cfg) {                         // L69-72
-       app.Errorf(...)
-       os.Exit(1)
-   }
    ...
-   aiServiceManager := app.NewAIServiceManager(aiServiceCfg)  // L87
-   if err := aiServiceManager.Start(appCtx); err != nil {     // L88-91
-       ...
-   }
-   healthy := aiServiceManager.WaitForHealthy(...)            // L92-96
-   if !healthy { ... }
-   defer aiServiceManager.Stop()                              // L97
-   providerFactory := app.NewServiceProviderFactory(aiServiceCfg, aiServiceManager)  // L98
+   providerFactory := app.NewLocalProviderFactory()  // 使用本地 provider（后续可改为 Hub 中介）
```

> **关键判断**：chat-server 的 `Session` 使用 `ProviderFactory` 接口创建 ASR/LLM/TTS 客户端。当前有两个实现：
> - `LocalProviderFactory`：直接使用 doubao SDK 创建客户端（需要 API Key 配置） 
> - `ServiceProviderFactory`：通过 HTTP/WS 直连 ai-doubao 的 `/v1/*` 端点
>
> **过渡方案**：由于 chat-server 本身已经持有完整的 AI API Key 配置（`configx.json` 中的 `asr_s`/`chat`/`tts_s`），最简方案是切换为 `LocalProviderFactory`，让 chat-server 直接调用 doubao SDK。这消除了对 ai-doubao 的依赖，且不影响功能。
>
> **最终方案**（后续迭代）：实现 `HubMediatedProviderFactory`，通过 Hub 的 `/api/tool/call` 调用 ai-doubao 的标准工具。

#### [MODIFY] `services/chat-server/internal/app/config.go`

**移除 `ai_service` 配置验证中的强制约束**：

```diff
 func validateModelConfig(cfg *ModelConfig) error {
     ...
-    svc := cfg.EffectiveAIService()
-    if svc.Mode != "local" && svc.Mode != "service" {
-        return fmt.Errorf("ai_service.mode must be local or service")
-    }
-    if svc.Mode == "service" && svc.BaseURL == "" {
-        return fmt.Errorf("ai_service.baseUrl is required in service mode")
-    }
     return nil
 }
```

#### [DELETE] 以下文件不再需要：

- `services/chat-server/internal/app/ai_service_manager.go` (341 行) — ai-doubao 进程管理
- `services/chat-server/internal/app/service_provider_factory.go` (501 行) — 直连 ai-doubao 客户端
- `services/chat-server/internal/app/service_call_audit.go` (17 行) — 直连审计记录

---

### Phase 4: Hub — WS 代理动态化

#### [MODIFY] `hub/cmd/hub/main.go`

修改 `/api/tool/ws` handler (L264-297)，使其支持按 query 参数 `service_id` 动态路由：

```diff
 mux.HandleFunc("/api/tool/ws", func(w http.ResponseWriter, r *http.Request) {
     ...
     claims, err := app.ExtractJWTClaims(r, authService)
     ...
-    reg, ok := hubPlatform.GetService("chat-server")
+    targetService := strings.TrimSpace(r.URL.Query().Get("service_id"))
+    if targetService == "" {
+        targetService = "chat-server"  // 默认向后兼容
+    }
+    reg, ok := hubPlatform.GetService(targetService)
     if !ok {
-        http.Error(w, "chat service is not registered", http.StatusServiceUnavailable)
+        http.Error(w, targetService+" is not registered", http.StatusServiceUnavailable)
         return
     }
     ...
 })
```

> 此改造使 Hub 的 WS 代理从"硬编码指向 chat-server"变为"可按参数动态选择目标服务"，为未来 ASR 流通过 Hub 中介提供基础。

---

### Phase 5: ai-doubao — 冗余代码大清理

> [!WARNING]
> 删除约 4800+ 行冗余代码。这些文件是从 chat-server 硬拷贝过来的模板，ai-doubao 作为原子能力提供者完全不需要。

#### [DELETE] 以下文件：

| 文件 | 行数 | 删除原因 |
|------|------|----------|
| `services/ai-doubao/internal/app/session.go` | 1795 | 对话会话管理，原子能力无需 |
| `services/ai-doubao/internal/app/sqlite_store.go` | ~1000 | 数据库存储层，原子能力无需 |
| `services/ai-doubao/internal/app/pipeline.go` | 619 | LLM→TTS 管道编排，原子能力无需 |
| `services/ai-doubao/internal/app/hub_builtins.go` | ~200 | 内置工具定义，原子能力无需 |
| `services/ai-doubao/internal/app/auth.go` | ~100 | 认证逻辑，原子能力无需 |
| `services/ai-doubao/internal/app/operation_log.go` | ~100 | 操作日志，原子能力无需 |
| `services/ai-doubao/internal/app/provider_factory.go` | ~40 | ProviderFactory 接口 |
| `services/ai-doubao/internal/app/runtime_root.go` | ~50 | 运行时根路径工具 |

#### [KEEP] 以下文件必须保留：

| 文件 | 说明 |
|------|------|
| `asr.go` | DoubaoASRClient：核心 ASR 驱动 |
| `llm.go` | DoubaoLLMClient：核心 LLM 驱动 |
| `tts.go` | DoubaoTTSClient：核心 TTS 驱动 |
| `config.go` | 配置加载 |
| `logger.go` | 日志工具 |
| `ai_service_protocol.go` | 协议类型定义 |
| `context_meta.go` | 上下文元数据 |
| `id.go` | ID 生成工具 |
| `jsonutil.go` | JSON 工具 |
| `message_types.go` | 消息类型常量 |
| `time_semantics.go` | 时间工具 |
| `version.go` | 版本信息 |
| `public_config.go` | 公共配置 |
| `runtime_config.go` | 运行时配置 |
| `hub_platform.go` | **清理后保留**：只保留 `ServiceManifest`/`BuildAIServiceManifest` 等注册相关类型（约 100 行），删除 `HubPlatform` 完整实现（ai-doubao 不需要做 Hub） |

> `hub_platform.go` 需要特殊处理：当前 `main.go` L120 调用了 `app.BuildAIServiceManifest()`，此函数定义在 `hub_platform.go` 中。需要将其提取为独立的轻量文件或保留最小子集。

---

## 5. 变更文件清单总览

### 5.1 ai-doubao

| 操作 | 文件 | 说明 |
|------|------|------|
| MODIFY | `cmd/ai-doubao/main.go` | 修复 Streaming Bug + 新增 `/service/tool/exec` + 加载 serviceSecret |
| DELETE | `internal/app/session.go` | 冗余：对话会话管理 |
| DELETE | `internal/app/sqlite_store.go` | 冗余：数据库存储 |
| DELETE | `internal/app/pipeline.go` | 冗余：LLM→TTS 管道 |
| DELETE | `internal/app/hub_builtins.go` | 冗余：内置工具 |
| DELETE | `internal/app/auth.go` | 冗余：认证逻辑 |
| DELETE | `internal/app/operation_log.go` | 冗余：操作日志 |
| DELETE | `internal/app/provider_factory.go` | 冗余：ProviderFactory |
| DELETE | `internal/app/runtime_root.go` | 冗余：运行时根路径 |
| MODIFY | `internal/app/hub_platform.go` | 精简：只保留 Manifest 相关类型 |

### 5.2 chat-server

| 操作 | 文件 | 说明 |
|------|------|------|
| MODIFY | `cmd/chat-server/main.go` | 移除 AIServiceManager 初始化、切换为 LocalProviderFactory、修复 Streaming Bug |
| MODIFY | `internal/app/config.go` | 移除 ai_service.mode 强制验证 |
| DELETE | `internal/app/ai_service_manager.go` | 不再由 chat-server 管理 ai-doubao 进程 |
| DELETE | `internal/app/service_provider_factory.go` | 不再直连 ai-doubao |
| DELETE | `internal/app/service_call_audit.go` | 直连审计记录不再需要 |

### 5.3 Hub

| 操作 | 文件 | 说明 |
|------|------|------|
| MODIFY | `cmd/hub/main.go` | `/api/tool/ws` 动态路由（支持 `service_id` query 参数） |

---

## 6. 验证与质量保证

### 6.1 编译验证

```bash
cd /Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent && go build ./...
```

### 6.2 现有测试回归

```bash
cd /Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent && go test ./hub/...
```

现有测试文件（6 个）必须完全通过：
- `hub/internal/app/identity_test.go`
- `hub/internal/routing/engine_test.go`
- `hub/internal/routing/schema_test.go`
- `hub/internal/security/headers_test.go`
- `hub/internal/supervisor/registry_test.go`
- `hub/internal/transport/client_test.go`

### 6.3 Streaming 注册验证

部署后检查 Hub 的 `/api/admin/services/tools` 接口：
- `ai.speech.asr` 的 `streaming` = `"stream"` ✓ (之前是空)
- `ai.llm.stream` 的 `streaming` = `"stream"` ✓ (之前是空)
- `ai.speech.tts` 的 `streaming` = `""` ✓ (none 类型)

### 6.4 隔离性检查

```bash
# 不带 Hub Token 直连 ai-doubao /service/tool/exec → 预期 401
curl -X POST http://127.0.0.1:18081/service/tool/exec \
  -H 'Content-Type: application/json' \
  -d '{"tool_id":"ai.speech.tts","args":{"text":"test"}}' \
  -w '\n%{http_code}'
# 预期: 401
```

### 6.5 端到端验证（部署后）

1. **语音全链路测试**：WebUI 录音 → chat-server WS → LLM → TTS → 播放
2. **日志审计**：确认 Hub 日志中能看到 tool call 审计记录
3. **chat-server 独立性**：chat-server 在 ai-doubao 未启动时仍能正常启动（使用 LocalProviderFactory 直接调用 doubao SDK）

---

## 7. 风险与降级

| 风险 | 影响 | 降级方案 |
|------|------|----------|
| Streaming Bug 修复后 Hub 路由行为变化 | Hub 可能开始对流式工具采用不同的转发策略 | 先验证 Hub 路由引擎对 `streaming=true` 的处理逻辑再部署 |
| 删除 ServiceProviderFactory 后 fallback 机制丢失 | `ServiceProviderFactory` 有 `fallback` 字段支持降级到本地 | `LocalProviderFactory` 本身就是"本地"模式，无需降级 |
| hub_platform.go 精简后编译失败 | ai-doubao main.go 依赖 `BuildAIServiceManifest` | 保留该函数及其最小依赖类型 |
| chat-server 无 ai_service 配置时的启动行为 | 旧配置文件中仍有 `ai_service` 段 | 移除强制验证后不会报错，配置段被忽略 |

---

## 8. 进度计划

| 阶段 | 内容 | 预估工时 |
|------|------|----------|
| Phase 1 | Streaming Bug 修复（ai-doubao + chat-server） | 10 min |
| Phase 2 | ai-doubao 新增 `/service/tool/exec` | 20 min |
| Phase 3 | chat-server 移除直连依赖 | 15 min |
| Phase 4 | Hub WS 代理动态化 | 10 min |
| Phase 5 | ai-doubao 冗余代码大清理 | 15 min |
| 验证 | 编译 + 测试 + 部署验证 | 15 min |

---

**核准人**: Antigravity (AI Architect)
**更新时间**: 2026-03-16 23:58 CST
