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

func renderSurfaceContent(messageType string, payload map[string]any) string {
	surfaceID := firstNonEmpty(asTrimmedString(payload["surface_id"]), asTrimmedString(payload["name"]), "surface")
	stateText := firstNonEmpty(asTrimmedString(payload["state_text"]), jsonCompactString(anyMap(payload["business_state"])), jsonCompactString(anyMap(payload["state"])))
	status := firstNonEmpty(asTrimmedString(payload["status"]), "unknown")
	eventType := firstNonEmpty(asTrimmedString(payload["event_type"]), "state_change")
	if stateText == "" {
		stateText = fmt.Sprintf("status=%s event=%s", status, eventType)
	}
	switch messageType {
	case TypeSurfaceOpen:
		return fmt.Sprintf("已打开 surface：%s。（surface_open name=%s status=%s）", surfaceID, surfaceID, status)
	case TypeSurfaceState:
		return fmt.Sprintf("%s 当前状态：%s。（surface_state name=%s status=%s state=%s）", surfaceID, stateText, surfaceID, status, stateText)
	default:
		return fmt.Sprintf("%s 发生变化：%s。（surface_change name=%s status=%s event=%s delta=%s）", surfaceID, stateText, surfaceID, status, eventType, stateText)
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

func formatActionReportText(report ActionReport) string {
	result := strings.TrimSpace(report.ResultSummary)
	if result == "" {
		result = "{}"
	}
	effect := strings.TrimSpace(report.EffectSummary)
	if effect == "" {
		effect = "{}"
	}
	tail := ""
	if report.ManualConfirm != "" {
		tail += " manual_confirm=" + report.ManualConfirm
	}
	if report.BlockReason != "" {
		tail += " block_reason=" + report.BlockReason
	}
	return fmt.Sprintf("[action_report] name=%s status=%s followup=%s result=%s effect=%s%s",
		report.ActionName, report.Status, normalizeFollowup(report.Followup), result, effect, tail)
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
