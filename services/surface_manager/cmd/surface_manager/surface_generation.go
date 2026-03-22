package main

import (
	"fmt"
	"strings"

	"kagent/pkg/hubsvc"
	app "kagent/services/surface_manager/internal/app"
)

func generateSurfaceByAI(hubToolCallURL string, bootstrap hubsvc.BootstrapSecret, callerServiceID string, surfaceName string, prompt string) (string, bool) {
	if strings.TrimSpace(hubToolCallURL) == "" || strings.TrimSpace(prompt) == "" {
		return "", false
	}
	systemPrompt := `你是一个 Surface scaffold 生成器。你必须只输出 JSON，格式为 {"files":{"相对路径":"文件内容"}}。
要求：
1. 只允许输出 manifest.json、index.html、README.md。
2. 必须兼容 Page -> Surface 的 postMessage/MessageChannel 握手模式。
3. surface 必须实现 surface_register -> surface_register_ack -> surface_ready，并能回报 state_change、action_result。
4. index.html 里必须至少支持 get_state 和一个可见业务动作，且优先复用 ../../../../lib/surfaceTool.js。
5. 不要输出 markdown 代码块，不要输出解释。`
	result, err := callHubToolAsService(hubToolCallURL, bootstrap, callerServiceID, "gen-"+app.NewRequestID(), "tr-"+app.NewRequestID(), "ai.llm.generate", map[string]any{
		"system_prompt": systemPrompt,
		"input":         fmt.Sprintf("surface_name=%s\n用户需求：%s", strings.TrimSpace(surfaceName), strings.TrimSpace(prompt)),
	})
	if err != nil || !result.Ok {
		return "", false
	}
	payload, _ := result.Result.(map[string]any)
	text, _ := payload["text"].(string)
	text = strings.TrimSpace(text)
	return text, text != ""
}
