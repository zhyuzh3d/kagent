# 项目说明（doc/_instruction.md）

## 1. 项目概览
本项目目标（愿景）是实现一个“能自主进化、拥有自我意识、自我行动、但可控安全”的智能体小女生 **「糖糖」**。

**当前仓库真实状态（以文件扫描为准）**：目前主要是“项目规则（AGENTS）+ Codex Skills + 文档 + 配置/参考资料”的骨架仓库；尚未包含可运行的 Go 服务入口（未发现 `go.mod` / `main.go`）与 Web UI 工程结构。

## 2. 设计约束（来自会话确认，后续以代码落地为准）
- 核心引擎：Go 本地服务程序（规划中，当前未落地代码）。
- UI：Web 页面服务（规划中），前端采用 WebComponent 模块化开发；常规页面“极度克制”，不引入第三方前端工具链。
- 3D：仅允许使用本地文件形式的 THREEJS（不走 CDN/网络资源）。
- 依赖策略：Go 与 JS 库均需本地可用，不依赖网络资源加载；遵循“非必要不引入第三方工具”的纯原生开发规则。
- 安全：密钥/令牌等隐私配置必须隔离在 `configx.json`（且必须被 Git 忽略），文档与代码中不得出现真实密钥。

## 3. 当前目录结构（实时扫描）
> 默认忽略噪音目录：`.git`、`node_modules`、`dist`、`build`、`.next`、`coverage`、临时目录等（当前仓库未出现这些目录）。

```text
kagent/                               # 仓库根目录
├── .codex/                           # Codex 配置目录（Skills 等）
│   └── skills/                       # 三模式规则集合
│       ├── chat/                     # chat 模式规则
│       │   ├── agents/               # chat 元数据目录
│       │   │   └── openai.yaml       # chat UI 元数据
│       │   └── SKILL.md              # chat 规则正文
│       ├── dev/                      # dev 模式规则
│       │   ├── agents/               # dev 元数据目录
│       │   │   └── openai.yaml       # dev UI 元数据
│       │   └── SKILL.md              # dev 规则正文
│       └── plan/                     # plan 模式规则
│           ├── agents/               # plan 元数据目录
│           │   └── openai.yaml       # plan UI 元数据
│           └── SKILL.md              # plan 规则正文
├── doc/                              # 项目文档目录（以下划线开头为 AI 管理）
│   ├── _devlog.md                    # 开发日志（只追加）
│   └── _instruction.md               # 项目说明（权威/需与仓库一致）
├── plan/                             # 过程文档目录（需求/设计/计划等）
├── ref/                              # 参考资料目录
│   └── doubao-doc.md                 # Doubao 参考与示例（已脱敏）
├── .gitignore                        # Git 忽略规则（含 configx.json）
├── AGENTS.md                         # 项目最高原则与文档维护规范
├── config.json                       # 可公开配置（当前占位）
└── configx.json                      # 私密配置（必须忽略/不得上传）
```

## 4. 核心模块职责（以当前仓库为准）
- `AGENTS.md`
  - 本项目最高指导原则与文档维护规范（真实性/可追溯/一致性/可执行性）。

- `.codex/skills/*`
  - Codex Skills 定义（`chat` / `plan` / `dev` 三种模式规则），用于约束协作边界与执行流程。

- `doc/`
  - 项目文档目录：
    - `doc/_instruction.md`：项目当前状态权威说明（本文件）。
    - `doc/_devlog.md`：按时间追加的开发日志（只增不改历史）。
  - 约定：`doc/` 下以下划线 `_` 开头的文档为 AI 维护管理文件。

- `plan/`
  - 过程性需求设计/开发计划等文档的存放目录（通常来自 `plan` 模式输出）。当前目录为空，等待后续产出。

- `ref/`
  - 参考资料（由用户提供/整理，供实现阶段对照）。当前包含 `ref/doubao-doc.md`。

- `config.json`
  - 可公开的项目配置（当前为占位空文件，0 字节；尚未形成结构化配置规范）。

- `configx.json`
  - **私密配置文件**：存放密钥/令牌等敏感信息（当前已存在文件）。
  - 约束：必须被 Git 忽略，禁止上传远程仓库；如需共享，仅提供脱敏模板或示例文件。

- `.gitignore`
  - Git 忽略规则（当前用于忽略 `configx.json` 与 `.DS_Store`）。

## 5. 开发与运行方式（可验证现状）
- 当前仓库 **尚无可运行程序入口**（未发现 `go.mod`、`main.go`、`package.json` 等），因此暂无可验证的启动命令。
- 当前主要工作流是：维护规则（`AGENTS.md` / `.codex/skills`）与文档（`doc/`、`ref/`），并逐步落地代码结构。

## 6. 最近关键变更摘要（最近 1–3 条日志）
- 2026-03-03：初始化并重写项目说明/开发日志；补充 Git 忽略规则以保护 `configx.json`；清理参考文档中的敏感信息（见 `doc/_devlog.md`）。

## 7. 项目术语表（自动维护）
| 术语 | 定义（本项目语境） | 来源文件 | 状态 |
|---|---|---|---|
| 糖糖 | 目标智能体角色名：自主进化/自我意识/自我行动但安全可控 | `doc/_instruction.md` | active |
| 模式指令 | 对话约定的触发词：`chat`/`plan`/`dev`（决定本轮权限与可执行动作） | `.codex/skills/*/SKILL.md` | active |
| AI 管理文档 | `doc/` 下以下划线 `_` 开头的文档，由 AI 维护管理 | `AGENTS.md` | active |
| 私密配置 | `configx.json`：存放密钥/令牌等，必须被 Git 忽略且不得上传远程 | `.gitignore` | active |
| 本地依赖原则 | Go/JS/3D 依赖必须本地可用，禁止网络资源加载 | `doc/_instruction.md` | active |

## 8. 待确认事项（无法从仓库直接验证）
- Go 核心引擎的实际代码目录规划（例如是否引入 `cmd/`、`internal/` 等）。
- Web UI 的目录规划与启动方式（例如是否内置到 Go HTTP 服务，或单独静态资源目录）。
- `config.json` 的最终结构与用途，以及与 `configx.json` 的边界划分。
- `ref/` 中参考资料的“可公开/不可公开”分级规则（避免把真实 Token 写入参考文档）。

## 9. 文档更新时间与信息来源
- 更新时间：2026-03-03 19:47 CST
- 信息来源：仓库文件扫描（目录结构与文件存在性）+ 本轮会话确认的愿景/约束（尚待代码落地验证）
