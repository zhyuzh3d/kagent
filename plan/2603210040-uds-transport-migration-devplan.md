# kagent 全链路传输层升级：从 TCP 到 UDS 的全局切换架构计划

## 1. 背景与目标
目前 kagent 的 Hub 与 Service 之间采用 TCP (HTTP/WebSockets) 进行本地通信。为了进一步提升性能、增强本地访问的安全性并消除端口冲突，计划实现全链路对 **Unix Domain Sockets (UDS)** 的支持，并提供通过 Hub 启动参数进行全局切换的能力。

### 核心目标
*   **全局开关**：Hub 启动时通过 `--proto=uds` 或 `--proto=tcp` 一键切换。
*   **代码解耦**：业务工具代码（Tool Logic）无需感知底层传输变化。
*   **透明适配**：利用 `pkg/hubsvc` 封装传输工厂，实现全量服务的极简适配。

---

## 2. 核心设计：传输层抽象 (Transport Abstraction)

在 `pkg/hubsvc` 中引入新的 `transport.go` 模块，提供统一的监听器和客户端工厂。

### 2.1 协议约定规范
*   **TCP 模式**：
    *   Endpoint 格式：`http://127.0.0.1:18080/`
    *   通信介质：Loopback Network Stack
*   **UDS 模式**：
    *   Endpoint 格式：`unix:///tmp/kagent/run/[service_id].sock`
    *   通信介质：内核内存缓冲区 (Kernel Memory Buffer)
    *   **WebSocket 适配**：由于 WebSocket 是基于字节流的，通过自定义 `Dialer` 可以在 UDS 上完美运行。

### 2.2 `hubsvc` 扩展接口 (伪代码示意)
```go
// pkg/hubsvc/transport.go

// 获取统一监听器
func NewListener(network, addr string) (net.Listener, error) {
    if network == "unix" {
        _ = os.Remove(addr) // 清理残留
        return net.Listen("unix", addr)
    }
    return net.Listen("tcp", addr)
}

// 获取感知协议的 HTTP 客户端
func NewHttpClient(proto string) *http.Client {
    if proto == "uds" {
        return &http.Client{
            Transport: &http.Transport{
                DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
                    // 工具 ID 或 Service ID 映射到具体的 .sock 路径
                    return (&net.Dialer{}).DialContext(ctx, "unix", getSockPath())
                },
            },
        }
    }
    return http.DefaultClient
}
```

---

## 3. 分阶段实施计划

### 第一阶段：`pkg/hubsvc` 基建升级
1.  创建 `pkg/hubsvc/transport.go`。
2.  实现 `TransportConfig` 结构，解析 `--proto` 环境变量或启动参数。
3.  封装 `UnifiedListener`，处理 UDS 文件的创建权限（0666）和退出后的自动清理动作。
4.  封装 `UdsHttpClient`，支持通过假域名或自定义协议头路由到正确的 Unix Socket。

### 第二阶段：Hub 调度器升级
1.  **参数注入**：Hub 启动时解析 `--proto`。
2.  **环境透传**：在 `LifecycleManager` 启动服务进程时，通过环境变量 `KAGENT_TRANSPORT_PROTO=uds` 将决策下传给各 Service。
3.  **网络决策**：
    *   如果是 UDS，Hub 自身的 Gateway 监听在 `hub.sock`。
    *   Hub 拨号所有 Service 时使用 UDS 模式。

### 第三阶段：全量 Service 适配
1.  **统一修改组件**：
    *   修改各服务的 `main.go` 入口。
    *   将 `http.ListenAndServe` 替换为 `hubsvc.Serve(ln, handler)`。
2.  **心跳同步**：
    *   Service 上报的 `Endpoint` 在 UDS 模式下自动转为 `unix://...`。
3.  **流式通信适配**：
    *   确保 `autogui` 和 `chat_server` 的 WebSocket 拨号逻辑通过 `hubsvc` 代理。

---

## 4. 关键技术评估
*   **性能提升**：预计本地调用延迟将降低 20%-40%。
*   **开发成本**：底层适配完成后，上层 99% 的代码（Tool 实现、Schema 定义）保持完全一致。
*   **向后兼容**：默认仍使用 TCP 协议，确保不改变环境的用户无感。

---

## 5. 验收标准
1.  使用 `hub --proto=uds` 启动系统。
2.  确认 `./run/` 目录下生成各服务的 `.sock` 文件。
3.  Admin UI 的“工具列表”能正常展示，且点击“详情”能通过 UDS 连接获取 Schema。
4.  运行烟测试（Smoke Test），验证跨服务的工具链调用无误。
