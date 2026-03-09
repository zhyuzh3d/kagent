package app

import "testing"

func TestShouldInterruptOnStartListen(t *testing.T) {
	if !shouldInterruptOnStartListen(StateSpeaking) {
		t.Fatalf("expected start_listen to interrupt while speaking")
	}
	if shouldInterruptOnStartListen(StateThinking) {
		t.Fatalf("did not expect start_listen to interrupt while thinking")
	}
	if shouldInterruptOnStartListen(StateListening) {
		t.Fatalf("did not expect start_listen to interrupt while listening")
	}
}

func TestShouldInterruptForRecognizedSpeech(t *testing.T) {
	if !shouldInterruptForRecognizedSpeech(StateThinking, 2, 3, "你好") {
		t.Fatalf("expected confirmed speech to interrupt a thinking turn")
	}
	if !shouldInterruptForRecognizedSpeech(StateSpeaking, 2, 3, "你好") {
		t.Fatalf("expected confirmed speech to interrupt a speaking turn")
	}
	if shouldInterruptForRecognizedSpeech(StateListening, 2, 3, "你好") {
		t.Fatalf("did not expect interruption while already listening")
	}
	if shouldInterruptForRecognizedSpeech(StateThinking, 2, 2, "你好") {
		t.Fatalf("did not expect interruption for the same generated turn")
	}
	if shouldInterruptForRecognizedSpeech(StateThinking, 2, 3, "   ") {
		t.Fatalf("did not expect interruption for empty recognized text")
	}
}
