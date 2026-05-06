# AlayaCore

[English](README.md) | [中文](README.zh-CN.md)

一个快速、极简的 AI Agent，运行在终端中。

**TUI 模式** — 分栏界面，支持流式输出、Vim 导航和会话管理。

![AlayaCore demo](misc/alayacore-demo.gif)

**Plain IO 模式** — 通过 stdin/stdout 支持脚本、管道和非交互式使用。

![AlayaCore plainio demo](misc/alayacore-demo-plainio.gif)

AlayaCore 可连接任何兼容 OpenAI 或 Anthropic 的 LLM，并为其提供读取、写入、编辑文件和执行命令的能力——全部通过支持流式输出、会话持久化和多步骤智能工具调用循环的交互式 TUI 完成。

## 快速开始

**方式一：从 [GitHub Releases](https://github.com/alayacore/alayacore/releases) 下载**

下载对应平台的二进制文件，解压后将其添加到 `PATH` 环境变量中。

**方式二：使用 Go 安装**

```sh
go install github.com/alayacore/alayacore@latest
```

然后运行 `alayacore`。

首次运行时，AlayaCore 会自动在 `~/.alayacore/model.conf` 创建默认模型配置，预配置为 Ollama。你可以编辑该文件以指向你偏好的提供商——在终端中按 `Ctrl+L` 然后按 `e`，或直接编辑文件。

## 功能特性

- **自主工具调用循环** — LLM 规划、调用工具并迭代，直到任务完成。每次提示最多支持 100 个步骤。
- **五种内置工具** — `read_file`、`edit_file`、`write_file`、`execute_command`、`search_content`（需安装 ripgrep `rg`）。
- **跨平台** — 支持 Linux、macOS 和 Windows。`execute_command` 工具可自动检测 shell（Unix 上为 bash/zsh/sh，Windows 上为 PowerShell/cmd）。
- **支持任何 LLM 提供商** — OpenAI、Anthropic、DeepSeek、Qwen、Ollama、LM Studio。一个配置文件支持多个模型，运行时可切换。
- **流式 TUI** — 实时输出，支持虚拟滚动、可折叠窗口和类 Vim 快捷键。
- **Plain IO 模式** — `--plainio` 用于脚本和管道。无 TUI，仅 stdin/stdout。
- **会话持久化** — 支持保存和恢复对话，自动保存。
- **技能系统** — 可按照 [Agent Skills](https://agentskills.io) 规范扩展指令包来增强 Agent 能力。
- **主题** — 可自定义配色方案，支持实时切换。

## 文档

| 文档 | 说明 |
|------|------|
| [快速入门](docs/getting-started.md) | 安装、CLI 参数和使用示例 |
| [配置](docs/configuration.md) | 模型配置、运行时配置和主题 |
| [终端 UI](docs/tui.md) | 快捷键、命令、窗口、任务队列 |
| [Plain IO 模式](docs/plainio.md) | 用于脚本和管道的 stdin/stdout |
| [技能系统](docs/skills.md) | Agent Skills 规范、目录结构、SKILL.md 格式 |
| [架构](docs/architecture.md) | 分层架构、TLV 协议、数据流、设计决策 |

## 许可证

MIT
