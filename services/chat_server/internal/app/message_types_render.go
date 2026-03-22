package app

import (
	"fmt"
	"strings"
)

func messageVisibility(role string, category string, messageType string) string {
	_ = category
	_ = messageType
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleUser, RoleAssistant:
		return "visible"
	default:
		return "hidden"
	}
}

func isAnchorMessage(msg ChatMessage) bool {
	if msg.Role != RoleUser && msg.Role != RoleAssistant {
		return false
	}
	if strings.TrimSpace(msg.Content) != "" || strings.TrimSpace(msg.Say) != "" {
		return true
	}
	return strings.TrimSpace(msg.RawData) != ""
}

func isUIVisibleMessage(msg ChatMessage) bool {
	return messageVisibility(msg.Role, msg.Category, msg.MessageType) == "visible"
}

func semanticPromptContent(msg ChatMessage) string {
	content := strings.TrimSpace(composeMessageContent(msg.Say, msg.Aside))
	if content == "" {
		content = strings.TrimSpace(msg.Content)
	}
	actionJSON := strings.TrimSpace(msg.ActionJSON)
	if actionJSON != "" {
		if content == "" {
			content = "[action] " + actionJSON
		} else {
			content += "\n[action] " + actionJSON
		}
	}
	if strings.TrimSpace(content) == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if msg.CreatedAtLocalYMDHMS != "" {
		parts = append(parts, msg.CreatedAtLocalYMDHMS)
	}
	if msg.CreatedAtLocalWeekday != "" {
		parts = append(parts, msg.CreatedAtLocalWeekday)
	}
	if msg.CreatedAtLocalLunar != "" {
		parts = append(parts, msg.CreatedAtLocalLunar)
	}
	if len(parts) == 0 {
		return content
	}
	return strings.Join(parts, " ") + " " + content
}

func renderMessageContent(category string, messageType string, payload map[string]any) string {
	switch category {
	case CategoryChat:
		return firstNonEmpty(asTrimmedString(payload["say"]), asTrimmedString(payload["text"]), asTrimmedString(payload["content"]), asTrimmedString(payload["aside"]))
	case CategoryAIAction, CategoryUserAction:
		return renderActionContent(category, messageType, payload)
	case CategorySurface:
		return renderSurfaceContent(messageType, payload)
	case CategorySurfaceContext:
		return renderSurfaceContextContent(messageType, payload)
	case CategoryPhase:
		return renderPhaseContent(messageType)
	case CategoryConfig:
		return renderConfigContent(payload)
	case CategoryError:
		return renderErrorContent(messageType, payload)
	default:
		return firstNonEmpty(asTrimmedString(payload["text"]), jsonCompactString(payload))
	}
}

func renderActionContent(category string, messageType string, payload map[string]any) string {
	name := firstNonEmpty(asTrimmedString(payload["action_name"]), asTrimmedString(payload["name"]), "unknown_action")
	followup := normalizeFollowup(asTrimmedString(payload["followup"]))
	argsText := jsonCompactString(anyMap(payload["args"]))
	resultText := firstNonEmpty(asTrimmedString(payload["result_summary"]), jsonCompactString(anyMap(payload["result"])))
	effectText := firstNonEmpty(asTrimmedString(payload["effect_summary"]), jsonCompactString(anyMap(payload["effect"])))
	status := firstNonEmpty(asTrimmedString(payload["status"]), "unknown")
	switch messageType {
	case TypeActionCall:
		return fmt.Sprintf("准备执行动作：%s。（%s.call name=%s followup=%s args=%s）", name, category, name, followup, argsText)
	case TypeActionExecute:
		return fmt.Sprintf("开始执行动作：%s。（%s.execute name=%s args=%s）", name, category, name, argsText)
	case TypeActionProgress:
		return fmt.Sprintf("动作执行中：%s。（%s.progress name=%s status=%s）", name, category, name, status)
	case TypeActionCombined:
		return fmt.Sprintf("动作已执行：%s。（%s.combined name=%s status=%s followup=%s result=%s effect=%s）", name, category, name, status, followup, resultText, effectText)
	default:
		return fmt.Sprintf("动作执行%s：%s。（%s.report name=%s status=%s followup=%s result=%s effect=%s）", humanizeActionStatus(status), name, category, name, status, followup, resultText, effectText)
	}
}

func observerSourceLabel(payload map[string]any, fallback string) string {
	if payload == nil {
		return strings.TrimSpace(fallback)
	}
	label := firstNonEmpty(
		asTrimmedString(payload["source_label"]),
		asTrimmedString(payload["surface_title"]),
		asTrimmedString(payload["surface_name"]),
	)
	if strings.TrimSpace(label) == "" {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(label)
}

func runtimeActionCount(runtime map[string]any) int {
	if runtime == nil {
		return 0
	}
	switch raw := runtime["actions"].(type) {
	case []any:
		return len(raw)
	case []map[string]any:
		return len(raw)
	default:
		return 0
	}
}

func renderSurfaceContent(messageType string, payload map[string]any) string {
	surfaceID := firstNonEmpty(asTrimmedString(payload["surface_id"]), asTrimmedString(payload["name"]), "surface")
	surfaceName := firstNonEmpty(asTrimmedString(payload["surface_title"]), asTrimmedString(payload["surface_name"]), surfaceID)
	sourceLabel := observerSourceLabel(payload, surfaceName)
	stateText := firstNonEmpty(
		asTrimmedString(payload["state_text"]),
		asTrimmedString(payload["visible_text"]),
		jsonCompactString(anyMap(payload["business_state"])),
		jsonCompactString(anyMap(payload["state"])),
	)
	status := firstNonEmpty(asTrimmedString(payload["status"]), "unknown")
	eventType := firstNonEmpty(asTrimmedString(payload["event_type"]), "state_change")
	if stateText == "" {
		stateText = fmt.Sprintf("status=%s event=%s", status, eventType)
	}
	switch messageType {
	case TypeSurfaceOpen:
		return fmt.Sprintf("%s 已打开。", sourceLabel)
	case TypeSurfaceState:
		return fmt.Sprintf("%s 当前状态变更：%s。", sourceLabel, stateText)
	default:
		if eventType == "surface_close" || eventType == "surface_closed" {
			return fmt.Sprintf("%s 已关闭。", sourceLabel)
		}
		return fmt.Sprintf("%s 发生变化：%s。", sourceLabel, stateText)
	}
}

func renderSurfaceContextContent(messageType string, payload map[string]any) string {
	sourceLabel := observerSourceLabel(payload, ObserverSourceLabelPage)
	switch messageType {
	case TypeSurfaceRegistrySync:
		registry, _ := payload["registry"].([]any)
		names := make([]string, 0, len(registry))
		for _, item := range registry {
			if row, ok := item.(map[string]any); ok {
				name := firstNonEmpty(asTrimmedString(row["name"]), asTrimmedString(row["surface_id"]))
				if name != "" {
					names = append(names, name)
				}
			}
		}
		if len(names) == 0 {
			return fmt.Sprintf("%s 已同步可用 surface 列表（0 个）。", sourceLabel)
		}
		if len(names) <= 3 {
			return fmt.Sprintf("%s 已同步可用 surface 列表（%d 个）：%s。", sourceLabel, len(names), strings.Join(names, "、"))
		}
		return fmt.Sprintf("%s 已同步可用 surface 列表（%d 个）。", sourceLabel, len(names))
	case TypeSurfaceActiveChange:
		active := firstNonEmpty(asTrimmedString(payload["active_surface_id"]), "none")
		if active == "none" {
			return fmt.Sprintf("%s 当前没有激活 surface。", sourceLabel)
		}
		return fmt.Sprintf("%s 切换激活 surface：%s。", sourceLabel, active)
	case TypeSurfaceRuntimeContext:
		runtime := anyMap(payload["runtime_context"])
		surfaceID := firstNonEmpty(asTrimmedString(payload["surface_id"]), asTrimmedString(runtime["surface_id"]), "surface")
		title := firstNonEmpty(asTrimmedString(runtime["title"]), asTrimmedString(payload["surface_title"]), asTrimmedString(payload["surface_name"]), surfaceID)
		sourceLabel = observerSourceLabel(payload, title)
		open := asTrimmedString(runtime["mode"]) != "closed"
		if raw, ok := runtime["open"].(bool); ok {
			open = raw
		}
		if !open {
			return fmt.Sprintf("%s 当前未打开。", sourceLabel)
		}
		mode := firstNonEmpty(asTrimmedString(runtime["mode"]), "floating")
		ready := "not_ready"
		if raw, ok := runtime["ready"].(bool); ok && raw {
			ready = "ready"
		}
		actionCount := runtimeActionCount(runtime)
		if actionCount > 0 {
			return fmt.Sprintf("%s 已同步运行上下文（mode=%s, ready=%s, actions=%d）。", sourceLabel, mode, ready, actionCount)
		}
		return fmt.Sprintf("%s 已同步运行上下文（mode=%s, ready=%s）。", sourceLabel, mode, ready)
	default:
		return firstNonEmpty(asTrimmedString(payload["text"]), jsonCompactString(payload))
	}
}

func renderPhaseContent(messageType string) string {
	switch messageType {
	case TypeConvoStart:
		return "对话开始。"
	case TypeConvoStop:
		return "对话停止。"
	case TypePageClose:
		return "页面关闭。"
	case TypeTurnNack:
		return "本轮输入无有效文本。"
	default:
		return "阶段事件。"
	}
}

func renderConfigContent(payload map[string]any) string {
	paths := stringSlice(payload["changed_paths"])
	source := firstNonEmpty(asTrimmedString(payload["source"]), "unknown")
	if len(paths) == 0 {
		return fmt.Sprintf("配置已更新。（config_change source=%s）", source)
	}
	label := paths[0]
	if len(paths) > 1 {
		label = fmt.Sprintf("%s 等 %d 项", paths[0], len(paths))
	}
	return fmt.Sprintf("配置已更新：%s。（config_change source=%s paths=%s）", label, source, jsonCompactString(paths))
}

func renderErrorContent(messageType string, payload map[string]any) string {
	text := firstNonEmpty(asTrimmedString(payload["message"]), asTrimmedString(payload["text"]), "unknown")
	switch messageType {
	case TypeWarningEvent:
		return fmt.Sprintf("系统警告：%s。", text)
	default:
		return fmt.Sprintf("系统错误：%s。", text)
	}
}

func humanizeActionStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "complete", "completed":
		return "成功"
	case "blocked":
		return "阻塞"
	case "cancelled", "canceled":
		return "取消"
	case "fail", "failed", "error":
		return "失败"
	default:
		return "完成"
	}
}

func mapProviderRole(roleInternal string) string {
	switch normalizeMessageRole(roleInternal) {
	case RoleObserver:
		return RoleSystem
	case RoleSystem:
		return RoleSystem
	case RoleUser:
		return RoleUser
	default:
		return RoleAssistant
	}
}
