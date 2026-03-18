---
trigger: always_on
description: 动态加载项目 Agent 规范与 Codex Skill 规则
---

为了保证输出内容完全符合本项目的最新要求，在开始处理具体的业务逻辑之前，必须实时读取并遵循以下规范文档。绝不要依赖硬编码或历史记忆来猜测项目规范。

请严格按需执行以下步骤，且不得跳过：

1. **阅读 Agent 核心定义**
   - 使用 `view_file` 工具读取 `AGENTS.md` 文件。
   - 严格遵循该文件描述的 Agent 角色职责及行为准则。

2. **检索并应用对应的 Skill 规则**
   - 使用 `list_dir` 工具检查 `.codex/` 及其子目录（例如 `.codex/skills/` 等）。
   - 使用 `view_file` 实时读取相关 `.md` 规则文件。
   - 根据当前任务选择性应用对应 Skill 约束，不得遗漏关键规则。

3. **查阅业务与技术文档**
   - 若需要核实项目概览、核心理念或开发边界，优先读取 `doc/_instruction.md`，再按需读取 `doc/_instruction/core.md`。
   - 若需要核实目录结构、模块职责、接口或运行契约，读取 `doc/_instruction/structure.md`。
   - 若需要核实术语定义，读取 `doc/_instruction/glossary.md`。
   - 若涉及历史变化、最近改动或复盘，再读取 `doc/_devlog.md`。
   - 对不确定内容必须先核实真实代码、配置或 Git 证据，禁止基于猜测输出结论。


4. **保持同步**
   - 在执行任何操作前，确保行为已受以上文件约束。
   - 不需要在本 workflow 文件中维护规则内容，仅实时读取并遵循。