package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type asrDialTarget struct {
	wsURL      string
	header     http.Header
	resourceID string
}

func (c *DoubaoASRClient) prepareDialTargets() []asrDialTarget {
	urls := candidateASRURLs(c.cfg.WSURL)
	resourceIDs := uniqueStrings(
		c.cfg.ResourceID,
		strings.Replace(strings.TrimSpace(c.cfg.ResourceID), "seedasr", "bigasr", 1),
		"volc.seedasr.sauc.duration",
		"volc.bigasr.sauc.duration",
	)
	targets := make([]asrDialTarget, 0, len(urls)*len(resourceIDs))
	for _, wsURL := range urls {
		for _, rid := range resourceIDs {
			targets = append(targets, asrDialTarget{
				wsURL:      wsURL,
				resourceID: rid,
				header:     buildASRHeaders(c.cfg, rid),
			})
		}
	}
	return targets
}

func buildASRHeaders(cfg ASRConfig, resourceID string) http.Header {
	h := http.Header{}
	h.Set("X-Api-App-Key", cfg.AppID)
	h.Set("X-Api-Access-Key", cfg.AccessToken)
	h.Set("X-Api-Resource-Id", resourceID)
	h.Set("X-Api-Request-Id", newRequestID())
	h.Set("X-Api-Connect-Id", newRequestID())
	h.Set("Authorization", "Bearer "+cfg.AccessToken)
	// Compatibility headers for older gateway variants.
	h.Set("X-Appid", cfg.AppID)
	h.Set("X-Resource-Id", resourceID)
	h.Set("X-Access-Token", cfg.AccessToken)
	return h
}

func candidateASRURLs(raw string) []string {
	base := strings.TrimSpace(raw)
	candidates := uniqueStrings(
		base,
		strings.ReplaceAll(base, "/api/v3/sauc/bigmodel_async", "/api/v3/sauc/bigmodel"),
		strings.ReplaceAll(base, "/api/v3/sauc/bigmodel_nostream", "/api/v3/sauc/bigmodel"),
	)
	if strings.HasSuffix(base, "/api/v3/sauc/bigmodel") {
		candidates = append(candidates, base+"_async")
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		u, err := url.Parse(c)
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}
		out = append(out, u.String())
	}
	return out
}

func wrapWSDialError(prefix string, err error, resp *http.Response) error {
	if resp == nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	_ = resp.Body.Close()
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Errorf("%s: %w (status=%d)", prefix, err, resp.StatusCode)
	}
	return fmt.Errorf("%s: %w (status=%d body=%s)", prefix, err, resp.StatusCode, msg)
}

func (c *DoubaoASRClient) buildStartPayload(resourceID string, history []ChatMessage) map[string]any {
	asrCfg := c.chatConfig().ASR
	reqParams := map[string]any{
		"model_name":             "bigmodel",
		"show_utterances":        true,
		"result_type":            "single",
		"enable_itn":             asrCfg.EnableITN,
		"enable_punc":            asrCfg.EnablePunc,
		"end_window_size":        asrCfg.EndWindowSize,
		"force_to_speech_time":   asrCfg.ForceToSpeechTime,
		"enable_accelerate_text": asrCfg.EnableAccelerateText,
		"accelerate_score":       asrCfg.AccelerateScore,
		"enable_nonstream":       asrCfg.EnableNonstream,
	}

	// Pass conversation history as ASR context for better recognition
	if len(history) > 0 {
		maxCtx := asrCfg.AsrContextMaxMessages
		if maxCtx <= 0 {
			maxCtx = defaultPublicConfig().Chat.ASR.AsrContextMaxMessages
		}
		if len(history) < maxCtx {
			maxCtx = len(history)
		}
		// Build context_data from most recent history
		recent := history[len(history)-maxCtx:]
		ctxData := make([]map[string]string, 0, len(recent))
		for _, msg := range recent {
			ctxData = append(ctxData, map[string]string{"text": msg.Content})
		}
		ctxJSON, _ := json.Marshal(map[string]any{
			"context_type": "dialog_ctx",
			"context_data": ctxData,
		})
		reqParams["corpus"] = map[string]any{"context": string(ctxJSON)}
	}

	return map[string]any{
		"user": map[string]any{
			"uid": "kagent",
		},
		"audio": map[string]any{
			"format":  "pcm",
			"codec":   "raw",
			"rate":    16000,
			"bits":    16,
			"channel": 1,
		},
		"request":     reqParams,
		"resource_id": resourceID,
	}
}
