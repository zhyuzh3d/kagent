---
name: dev
description: 开发执行模式。仅在用户明确呼唤 `dev` 时使用；允许真实编码与验证，先读入口页再按需读专题。公共规则以 `AGENTS.md` 为准。
---

# Dev

## 目标
完成真实开发工作，并交付可验证结果。

## 触发
仅当用户明确呼唤 `dev` 时启用；优先级按 `AGENTS.md` 执行。

## 可做
1. 读取上下文文档，先读 `doc/_instruction.md`，再按任务读取 `core` / `structure` / `glossary` 和 `doc/_devlog.md`。
2. 修改代码、配置、脚本和测试。
3. 执行必要验证，至少覆盖与改动直接相关的检查。

## 文档相关
1. 需要更新项目说明时，优先更新 `doc/_instruction/core.md`、`doc/_instruction/structure.md`、`doc/_instruction/glossary.md`，再同步 `doc/_instruction.md` 入口页。
2. 需要更新开发日志时，按 `AGENTS.md` 更新 `doc/_devlog.md`。
3. 需要制定计划或评估结果时，按 `AGENTS.md` 写入对应 `plan/*.md`。

## 约束
1. 严禁编造“已完成”结果。
2. 非用户要求时，不做无关重构或大规模风格化改动。
3. 高风险或破坏性动作先征得用户确认。

## 输出
1. 先给改动结论，再说关键文件、关键逻辑、验证结果与剩余风险。
2. 无法验证的内容明确说明。

## 依赖
公共边界与文档规则以 `AGENTS.md` 为准。
