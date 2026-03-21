# Kagent 项目说明入口

`doc/_instruction.md` 只保留最小入口信息，用来引导 agent 按任务读取更具体的子文档。

## 项目概览

`kagent` 是一个基于 `Hub + 多独立 Service` 的本地多进程 AI 交互与工具平台。当前说明文档按“入口 + 专题”拆分，避免每次都加载整份长文档。

## 推荐读取顺序

1. 先读本文件，确认项目概览和文档路由。
2. 需要核心理念、开发规范或当前边界时，读 [`doc/_instruction/core.md`](./_instruction/core.md)。
3. 需要目录结构、模块职责或关键接口时，读 [`doc/_instruction/structure.md`](./_instruction/structure.md)。
4. 需要前端页面设计美学与开发规范时，读 [`doc/_page_guide.md`](./_page_guide.md)。
5. 需要术语定义时，读 [`doc/_instruction/glossary.md`](./_instruction/glossary.md)。
6. 需要最近改动或历史演进时，读 [`doc/_devlog.md`](./_devlog.md)。

## 读取原则

- 默认先读入口，再按任务补读专题文档。
- `doc/_devlog.md` 只负责历史增量，不在说明文档里重复维护。
- 需要当前状态判断时，以专题文档 and 真实代码/Git 证据为准。

## 文档职责

- `doc/_instruction/core.md`：项目核心理念、开发规范、默认边界。
- `doc/_instruction/structure.md`：当前目录结构、核心模块职责、关键接口。
- `doc/_page_guide.md`：前端页面设计美学规范、交互模式、纯原生开发技术标准。
- `doc/_instruction/glossary.md`：术语表。
- `doc/_devlog.md`：开发记录与最近变化。

## 更新时间

**文档更新时间**：2026-03-21 11:38 CST

**信息来源**：仓库实时文件核验与前端设计规范下沉决策。
