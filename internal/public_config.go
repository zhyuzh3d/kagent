package app

type PublicConfig struct {
	App  AppPublicConfig  `json:"app"`
	Chat ChatPublicConfig `json:"chat"`
}

type AppPublicConfig struct {
	Debug AppDebugPublicConfig `json:"debug"`
	UI    AppUIPublicConfig    `json:"ui"`
}

type AppDebugPublicConfig struct {
	LogLevel string `json:"logLevel"`
}

type AppUIPublicConfig struct {
	ShowDebugPanelByDefault bool `json:"showDebugPanelByDefault"`
}

type ChatPublicConfig struct {
	Frontend ChatFrontendPublicConfig `json:"frontend"`
	Session  ChatSessionPublicConfig  `json:"session"`
	ASR      ChatASRPublicConfig      `json:"asr"`
	LLM      ChatLLMPublicConfig      `json:"llm"`
	TTS      ChatTTSPublicConfig      `json:"tts"`
	Pipeline ChatPipelinePublicConfig `json:"pipeline"`
}

type ChatFrontendPublicConfig struct {
	VoiceThreshold     float64 `json:"voiceThreshold"`
	UtteranceSilenceMs int     `json:"utteranceSilenceMs"`
	BargeInThreshold   float64 `json:"bargeInThreshold"`
	BargeInMinFrames   int     `json:"bargeInMinFrames"`
	BargeInCooldownMs  int     `json:"bargeInCooldownMs"`
	ReplyOnsetGuardMs  int     `json:"replyOnsetGuardMs"`
	PreRollMaxFrames   int     `json:"preRollMaxFrames"`
	SilentTailFrames   int     `json:"silentTailFrames"`
	FrameSamples16k    int     `json:"frameSamples16k"`
}

type ChatSessionPublicConfig struct {
	UpstreamAudioQueueSize int `json:"upstreamAudioQueueSize"`
	DownstreamTTSQueueSize int `json:"downstreamTTSQueueSize"`
	ControlQueueSize       int `json:"controlQueueSize"`
	TriggerLLMWaitFinalMs  int `json:"triggerLLMWaitFinalMs"`
	MaxHistoryMessages     int `json:"maxHistoryMessages"`
}

type ChatASRPublicConfig struct {
	EndWindowSize         int  `json:"endWindowSize"`
	ForceToSpeechTime     int  `json:"forceToSpeechTime"`
	AccelerateScore       int  `json:"accelerateScore"`
	EnableITN             bool `json:"enableITN"`
	EnablePunc            bool `json:"enablePunc"`
	EnableAccelerateText  bool `json:"enableAccelerateText"`
	EnableNonstream       bool `json:"enableNonstream"`
	AsrContextMaxMessages int  `json:"asrContextMaxMessages"`
	WriteTimeoutMs        int  `json:"writeTimeoutMs"`
	ReadTimeoutMs         int  `json:"readTimeoutMs"`
}

type ChatLLMPublicConfig struct {
	SystemPrompt    string `json:"systemPrompt"`
	StreamTimeoutMs int    `json:"streamTimeoutMs"`
}

type ChatTTSPublicConfig struct {
	VoiceType      string `json:"voiceType"`
	WriteTimeoutMs int    `json:"writeTimeoutMs"`
	ReadTimeoutMs  int    `json:"readTimeoutMs"`
}

type ChatPipelineGroupingPolicy struct {
	BacklogMs       int `json:"backlogMs"`
	TargetSentences int `json:"targetSentences"`
	MaxRunes        int `json:"maxRunes"`
}

type ChatPipelinePublicConfig struct {
	GroupingPolicies []ChatPipelineGroupingPolicy `json:"groupingPolicies"`
	SentenceBreaks   []string                     `json:"sentenceBreaks"`
	SpeechRuneMs     int                          `json:"speechRuneMs"`
	SentencePauseMs  int                          `json:"sentencePauseMs"`
	ClausePauseMs    int                          `json:"clausePauseMs"`
	MinimumSpeechMs  int                          `json:"minimumSpeechMs"`
	BacklogCapMs     int                          `json:"backlogCapMs"`
}

func defaultPublicConfig() PublicConfig {
	return PublicConfig{
		App: AppPublicConfig{
			Debug: AppDebugPublicConfig{LogLevel: "info"},
			UI:    AppUIPublicConfig{ShowDebugPanelByDefault: false},
		},
		Chat: ChatPublicConfig{
			Frontend: ChatFrontendPublicConfig{
				VoiceThreshold:     0.018,
				UtteranceSilenceMs: 500,
				BargeInThreshold:   0.08,
				BargeInMinFrames:   5,
				BargeInCooldownMs:  500,
				ReplyOnsetGuardMs:  1200,
				PreRollMaxFrames:   5,
				SilentTailFrames:   50,
				FrameSamples16k:    320,
			},
			Session: ChatSessionPublicConfig{
				UpstreamAudioQueueSize: 64,
				DownstreamTTSQueueSize: 24,
				ControlQueueSize:       32,
				TriggerLLMWaitFinalMs:  320,
				MaxHistoryMessages:     20,
			},
			ASR: ChatASRPublicConfig{
				EndWindowSize:         500,
				ForceToSpeechTime:     1000,
				AccelerateScore:       10,
				EnableITN:             true,
				EnablePunc:            true,
				EnableAccelerateText:  true,
				EnableNonstream:       false,
				AsrContextMaxMessages: 10,
				WriteTimeoutMs:        6000,
				ReadTimeoutMs:         60000,
			},
			LLM: ChatLLMPublicConfig{
				SystemPrompt:    "你是一个语音助手。请用简洁自然的口语风格回答，每次回复控制在2-3句话以内，避免使用列表、标题、markdown等格式化文本。",
				StreamTimeoutMs: 65000,
			},
			TTS: ChatTTSPublicConfig{
				VoiceType:      "",
				WriteTimeoutMs: 6000,
				ReadTimeoutMs:  35000,
			},
			Pipeline: ChatPipelinePublicConfig{
				GroupingPolicies: []ChatPipelineGroupingPolicy{
					{BacklogMs: 20000, TargetSentences: 10, MaxRunes: 500},
					{BacklogMs: 10000, TargetSentences: 5, MaxRunes: 200},
					{BacklogMs: 5000, TargetSentences: 3, MaxRunes: 50},
					{BacklogMs: 3000, TargetSentences: 2, MaxRunes: 24},
					{BacklogMs: 0, TargetSentences: 1, MaxRunes: 80},
				},
				SentenceBreaks:  []string{"。", "！", "？", "；", ".", "!", "?", ";", "…", "\n"},
				SpeechRuneMs:    180,
				SentencePauseMs: 220,
				ClausePauseMs:   90,
				MinimumSpeechMs: 400,
				BacklogCapMs:    60000,
			},
		},
	}
}
