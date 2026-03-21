package app

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Session) handleActionResult(ctrl ControlMessage) {
	turnID := s.turnID.Load()
	if ctrl.TurnID > 0 {
		turnID = ctrl.TurnID
	}
	actionName := strings.TrimSpace(ctrl.ActionName)
	if actionName == "" {
		s.emitError(turnID, "bad_action_result", "missing action_name in action_result", true)
		return
	}
	followup := normalizeFollowup(ctrl.ActionFollowup)
	actionID := firstNonEmpty(strings.TrimSpace(ctrl.ActionID), "act-"+newRequestID())
	status := firstNonEmpty(strings.TrimSpace(ctrl.ActionStatus), "unknown")
	manualConfirm := normalizeManualConfirm(ctrl.ActionManualConfirm)
	blockReason := normalizeBlockReason(ctrl.ActionBlockReason)
	if blockReason == "" {
		blockReason = s.evaluateActionGuard(actionName, ctrl.ActionArgs)
	}
	if blockReason != "" && manualConfirm == "" {
		manualConfirm = "waiting"
	}
	if manualConfirm == "cancel" {
		status = "cancelled"
	} else if manualConfirm == "waiting" && status == "ok" {
		status = "blocked"
	}

	surfaceID := firstNonEmpty(strings.TrimSpace(ctrl.ActionSurfaceID), inferSurfaceIDFromAction(actionName))
	resultSummary := summarizeActionResultForReport(strings.TrimSpace(ctrl.Text), status, ctrl.ActionResult)
	effectSummary := summarizeAnyMap(ctrl.ActionEffect)
	businessState := cloneAnyMap(ctrl.ActionState)
	if len(businessState) == 0 {
		businessState = extractBusinessState(ctrl.ActionEffect, ctrl.ActionResult)
	}
	now := nowMS()
	storeUserID := "default"
	storeProjectID := "project-default"
	storeThreadID := "chat-default"
	if s.chatStore != nil {
		storeUserID = s.chatStore.RuntimeUserID()
		storeProjectID = s.chatStore.RuntimeProjectID()
		storeThreadID = s.chatStore.RuntimeThreadID()
	}
	report := ActionReport{
		ReportID:       "rep-" + newRequestID(),
		Origin:         "action_callback",
		UserID:         storeUserID,
		ProjectID:      storeProjectID,
		ThreadID:       storeThreadID,
		TurnID:         turnID,
		SurfaceID:      surfaceID,
		SurfaceType:    firstNonEmpty(strings.TrimSpace(ctrl.SurfaceType), "app"),
		SurfaceVersion: firstNonEmpty(strings.TrimSpace(ctrl.SurfaceVersion), "1"),
		ActionID:       actionID,
		ActionName:     actionName,
		Followup:       followup,
		Status:         status,
		ResultSummary:  resultSummary,
		EffectSummary:  effectSummary,
		BusinessState:  businessState,
		ManualConfirm:  manualConfirm,
		BlockReason:    blockReason,
		CreatedAtMS:    now,
		CreatedAtISO:   time.UnixMilli(now).Format(time.RFC3339),
		MessageType:    "action_report",
		Visibility:     "hidden",
	}
	callMessageID := s.resolveActionCallRef(actionID, actionName)
	if callMessageID == "" {
		callPayload := map[string]any{
			"type":            TypeActionCall,
			"id":              actionID,
			"path":            actionName,
			"name":            actionName,
			"surface_id":      surfaceID,
			"surface_type":    report.SurfaceType,
			"surface_version": report.SurfaceVersion,
			"followup":        followup,
			"args":            cloneAnyMap(ctrl.ActionArgs),
			"status":          status,
			"manual_confirm":  manualConfirm,
			"block_reason":    blockReason,
			"trigger_reason":  firstNonEmpty(strings.TrimSpace(ctrl.Reason), "dispatch"),
		}
		callMessageID = s.appendHistoryMessage(ChatMessage{
			TurnID:      turnID,
			Role:        RoleObserver,
			Category:    CategoryAIAction,
			MessageType: TypeActionCall,
			Say:         "",
			ActionJSON:  mustJSON(callPayload),
			PayloadJSON: mustJSON(callPayload),
			CreatedAtMS: now,
		})
		if callMessageID != "" {
			s.bindActionCallRef(actionID, callMessageID)
		}
	}
	executePayload := map[string]any{
		"type":            TypeActionExecute,
		"ref_message_id":  callMessageID,
		"ref_action_slot": 0,
		"action_id":       actionID,
		"path":            actionName,
		"name":            actionName,
		"status":          "running",
		"dispatch_info": map[string]any{
			"surface_id":      surfaceID,
			"surface_type":    report.SurfaceType,
			"surface_version": report.SurfaceVersion,
			"trigger_reason":  firstNonEmpty(strings.TrimSpace(ctrl.Reason), "dispatch"),
		},
	}
	s.appendHistoryMessage(ChatMessage{
		TurnID:        turnID,
		Role:          RoleObserver,
		Category:      CategoryAIAction,
		MessageType:   TypeActionExecute,
		Say:           "",
		ActionJSON:    mustJSON(executePayload),
		RefMessageID:  callMessageID,
		RefActionSlot: 0,
		PayloadJSON:   mustJSON(executePayload),
		CreatedAtMS:   now,
	})

	reportText := formatActionReportText(report)
	reportPayload := map[string]any{
		"type":            TypeActionReport,
		"ref_message_id":  callMessageID,
		"ref_action_slot": 0,
		"report_id":       report.ReportID,
		"origin":          report.Origin,
		"action_id":       actionID,
		"path":            actionName,
		"name":            actionName,
		"surface_id":      surfaceID,
		"surface_type":    report.SurfaceType,
		"surface_version": report.SurfaceVersion,
		"followup":        followup,
		"state":           reportStateFromStatus(status),
		"status":          status,
		"desc":            resultSummary,
		"result_summary":  resultSummary,
		"effect_summary":  effectSummary,
		"result":          cloneAnyMap(ctrl.ActionResult),
		"effect":          cloneAnyMap(ctrl.ActionEffect),
		"business_state":  cloneAnyMap(businessState),
		"manual_confirm":  manualConfirm,
		"block_reason":    blockReason,
	}
	reportMsg := ChatMessage{
		TurnID:        turnID,
		Role:          RoleObserver,
		Category:      CategoryAIAction,
		MessageType:   TypeActionReport,
		Say:           "",
		ActionJSON:    mustJSON(reportPayload),
		RefMessageID:  callMessageID,
		RefActionSlot: 0,
		Content:       reportText,
		PayloadJSON:   mustJSON(reportPayload),
		CreatedAtMS:   now,
	}
	s.appendHistoryMessage(reportMsg)
	if callMessageID != "" {
		s.bindActionCallRef(actionID, callMessageID)
	}

	_ = s.sendEvent(EventMessage{
		Type:           "action_report",
		TsMS:           now,
		TurnID:         turnID,
		Origin:         "action_callback",
		MessageType:    "action_report",
		Text:           reportText,
		ActionID:       actionID,
		ActionName:     actionName,
		ActionStatus:   status,
		Followup:       followup,
		ManualConfirm:  manualConfirm,
		BlockReason:    blockReason,
		SurfaceID:      surfaceID,
		SurfaceType:    report.SurfaceType,
		SurfaceVersion: report.SurfaceVersion,
		BusinessState:  cloneAnyMap(businessState),
		Payload:        reportPayload,
	})
	Infof("[Turn:%d] action report generated: %s status=%s followup=%s", turnID, actionName, status, followup)
	if followup == "report" && manualConfirm != "waiting" && manualConfirm != "cancel" {
		s.enqueueFollowupMessage(reportMsg, concurrentActionHint(ctrl.ActionResult) > 1)
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

func summarizeActionResultForReport(contentText string, status string, result map[string]any) string {
	cleanText := strings.TrimSpace(contentText)
	if strings.EqualFold(strings.TrimSpace(status), "ok") {
		return firstNonEmpty(cleanText, summarizeAnyMap(result))
	}
	reason := firstNonEmpty(
		asTrimmedString(result["reason"]),
		asTrimmedString(result["error"]),
		asTrimmedString(result["message"]),
	)
	if reason != "" {
		return "失败原因：" + reason
	}
	return firstNonEmpty(cleanText, summarizeAnyMap(result))
}

func inferSurfaceIDFromAction(actionName string) string {
	name := strings.TrimSpace(actionName)
	lower := strings.ToLower(name)
	if lower == "get_surfaces" {
		return "surface_registry"
	}
	if strings.HasPrefix(lower, "surface.call.") {
		parts := strings.Split(name, ".")
		if len(parts) >= 4 {
			surfaceID := strings.TrimSpace(parts[2])
			if surfaceID != "" {
				return surfaceID
			}
		}
	}
	return ""
}

func summarizeAnyMap(v map[string]any) string {
	if len(v) == 0 {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func extractBusinessState(candidates ...map[string]any) map[string]any {
	for _, c := range candidates {
		if len(c) == 0 {
			continue
		}
		if raw, ok := c["business_state"]; ok {
			if m, ok := raw.(map[string]any); ok && len(m) > 0 {
				return cloneAnyMap(m)
			}
		}
		if raw, ok := c["state"]; ok {
			if m, ok := raw.(map[string]any); ok && len(m) > 0 {
				return cloneAnyMap(m)
			}
		}
	}
	return nil
}

func normalizeManualConfirm(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "confirm":
		return "confirm"
	case "cancel":
		return "cancel"
	case "waiting":
		return "waiting"
	default:
		return ""
	}
}

func normalizeBlockReason(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "rate_limit":
		return "rate_limit"
	case "quota_limit":
		return "quota_limit"
	default:
		return ""
	}
}

func (s *Session) evaluateActionGuard(actionName string, args map[string]any) string {
	now := time.Now().UnixMilli()
	const (
		windowMS = int64(60_000)
		rateMax  = 10
		dedupeMS = int64(3_000)
	)
	keyHash := sha1.Sum([]byte(actionName + "|" + summarizeAnyMap(args)))
	key := fmt.Sprintf("%x", keyHash[:])

	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	if s.actionDedup == nil {
		s.actionDedup = map[string]int64{}
	}

	filtered := make([]int64, 0, len(s.actionRateWindow)+1)
	for _, ts := range s.actionRateWindow {
		if now-ts <= windowMS {
			filtered = append(filtered, ts)
		}
	}
	if len(filtered) >= rateMax {
		s.actionRateWindow = filtered
		return "rate_limit"
	}
	if last, ok := s.actionDedup[key]; ok && now-last <= dedupeMS {
		s.actionRateWindow = append(filtered, now)
		s.actionDedup[key] = now
		return "quota_limit"
	}
	s.actionRateWindow = append(filtered, now)
	s.actionDedup[key] = now
	return ""
}
