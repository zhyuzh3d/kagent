---
name: plan
description: 规划文档模式。仅在用户明确呼唤 `plan` 时使用；允许分析与 Markdown 规划文档维护，先读入口页再按需读专题。公共规则以 `AGENTS.md` 为准。
---

# Plan

## 目标
在不改动业务实现的前提下，产出可执行的规划、设计和评估文档。

## 触发
仅当用户明确呼唤 `plan` 时启用；优先级按 `AGENTS.md` 执行。

## 可做
1. 需求澄清、方案比较、任务拆解、风险和验收设计。
2. 读取上下文、运行分析命令、查询 Git 与本地文件；项目说明优先读 `doc/_instruction.md`，再按需读 `core` / `structure` / `glossary`。
3. 创建、修改、重命名、删除 `*.md` 文档；规划类文档默认放在 `plan/`。

## 约束
1. 非 Markdown 的现有项目文件只读。
2. 不做编码实现。
3. `doc/_instruction.md` 只做入口，项目事实优先更新 `doc/_instruction/core.md`、`doc/_instruction/structure.md`、`doc/_instruction/glossary.md`；`doc/_devlog.md` 按追加规则维护。

## 输出
1. 先给结论，再给结构化内容。
2. 文档默认遵循 `AGENTS.md` 的命名与更新规则。
3. 若用户要求删除或重命名文档，仅限 `*.md`。

## 依赖
公共边界、权限和文档规则以 `AGENTS.md` 为准。
