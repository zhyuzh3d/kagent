package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

func BuildMessage(in MessageWrite) (ChatMessage, error) {
	role := normalizeMessageRole(in.Role)
	payloadVersion := in.PayloadSchemaVersion
	if payloadVersion <= 0 {
		payloadVersion = PayloadSchemaVersion1
	}
	payload := clonePayloadMap(in.Payload)
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON := strings.TrimSpace(in.PayloadJSON)
	if payloadJSON == "" {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return ChatMessage{}, fmt.Errorf("marshal payload: %w", err)
		}
		payloadJSON = string(encoded)
	} else if len(payload) == 0 {
		_ = json.Unmarshal([]byte(payloadJSON), &payload)
	}
	say := firstNonEmpty(strings.TrimSpace(in.Say), asTrimmedString(payload["say"]), asTrimmedString(payload["text"]), asTrimmedString(payload["content"]))
	aside := firstNonEmpty(strings.TrimSpace(in.Aside), asTrimmedString(payload["aside"]))
	actionFromPayload := ""
	if actionMap := anyMap(payload["action"]); len(actionMap) > 0 {
		b, err := json.Marshal(actionMap)
		if err == nil {
			actionFromPayload = string(b)
		}
	}
	actionJSON := normalizeActionJSON(firstNonEmpty(strings.TrimSpace(in.ActionJSON), actionFromPayload))
	rawData := normalizeRawDataJSON(strings.TrimSpace(in.RawData))
	parseError := firstNonEmpty(strings.TrimSpace(in.ParseError), asTrimmedString(payload["parse_error"]))

	content := strings.TrimSpace(in.Content)
	if content == "" {
		content = strings.TrimSpace(composeMessageContent(say, aside))
	}
	if content == "" {
		content = strings.TrimSpace(renderMessageContent(normalizeMessageCategory(in.Category), strings.ToLower(strings.TrimSpace(in.MessageType)), payload))
	}
	if content == "" && strings.TrimSpace(actionJSON) != "" {
		content = "[action]"
	}
	if content == "" {
		return ChatMessage{}, fmt.Errorf("message content is empty for role=%s", role)
	}

	actionType := detectActionTypeFromJSON(actionJSON)
	category := inferMessageCategory(normalizeMessageCategory(in.Category), actionType, role)
	messageType := normalizeMessageType(category, in.MessageType, role)
	if actionType != "" && strings.TrimSpace(in.MessageType) == "" {
		messageType = actionType
	}
	refActionSlot := in.RefActionSlot
	if refActionSlot < 0 {
		refActionSlot = 0
	}
	if refActionSlot == 0 {
		switch v := payload["ref_action_slot"].(type) {
		case float64:
			if v > 0 {
				refActionSlot = int(v)
			}
		case int:
			if v > 0 {
				refActionSlot = v
			}
		case int64:
			if v > 0 {
				refActionSlot = int(v)
			}
		}
	}
	createdAtMS := in.CreatedAtMS
	if createdAtMS <= 0 {
		createdAtMS = nowMS()
	}
	timeFields := buildSemanticTimeFields(createdAtMS)
	completionStatus := normalizeCompletionStatus(in.CompletionStatus)
	if role == RoleAssistant && category == CategoryChat && messageType == TypeAssistantMessage && completionStatus == "" {
		completionStatus = CompletionStatusComplete
	}
	interrupt := normalizeInterrupt(in.Interrupt)
	if completionStatus == "" {
		interrupt = ""
	} else if interrupt == "" {
		interrupt = InterruptNone
	}
	entry := ChatMessage{
		MessageID:             strings.TrimSpace(in.MessageID),
		TurnID:                in.TurnID,
		Seq:                   in.Seq,
		Role:                  role,
		Say:                   say,
		Aside:                 aside,
		ActionJSON:            actionJSON,
		RefMessageID:          firstNonEmpty(strings.TrimSpace(in.RefMessageID), asTrimmedString(payload["ref_message_id"])),
		RefActionSlot:         refActionSlot,
		RawData:               rawData,
		ParseError:            parseError,
		Category:              category,
		MessageType:           messageType,
		Content:               content,
		PayloadSchemaVersion:  payloadVersion,
		PayloadJSON:           payloadJSON,
		Visibility:            messageVisibility(role, category, messageType),
		CreatedAtMS:           createdAtMS,
		CreatedAtISO:          timeFields.ISO,
		CreatedAtLocalYMDHMS:  timeFields.LocalYMDHMS,
		CreatedAtLocalWeekday: timeFields.LocalWeekday,
		CreatedAtLocalLunar:   timeFields.LocalLunar,
		CompletionStatus:      completionStatus,
		Interrupt:             interrupt,
		InterruptAtMS:         in.InterruptAtMS,
		PartialText:           strings.TrimSpace(in.PartialText),
	}
	if entry.MessageID == "" {
		entry.MessageID = "msg-" + newRequestID()
	}
	return entry, nil
}
