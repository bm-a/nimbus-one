package tools

import (
	"context"
	"fmt"
	"strings"

	"nimbus-one/internal/media"
)

// TranscribeTool converts audio files (Telegram .ogg voice notes as-is)
// to text for the agent loop.
type TranscribeTool struct {
	// AllowDirs scopes the audio path; empty means no restriction
	// (voice notes arrive from channels, not just the workspace).
	AllowDirs []string
}

func (t *TranscribeTool) Name() string { return "transcribe" }

func (t *TranscribeTool) Description() string {
	return "Transcribe an audio file (.ogg/.opus/.m4a/.wav) to text via whisper.cpp or an OpenAI-compatible endpoint. No conversion needed."
}

func (t *TranscribeTool) Parameters() map[string]Param {
	return map[string]Param{
		"path": {Type: "string", Description: "Audio file path.", Required: true},
	}
}

func (t *TranscribeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("transcribe: missing required argument %q", "path")
	}
	if len(t.AllowDirs) > 0 {
		abs, err := resolveWithinAllow(path, t.AllowDirs)
		if err != nil {
			return "", fmt.Errorf("transcribe: %w", err)
		}
		path = abs
	}
	text, err := media.Transcribe(ctx, path, media.STTConfig{})
	if err != nil {
		return "", err
	}
	return text, nil
}

// SpeakTool plays text aloud (local engine) or renders an audio file.
type SpeakTool struct{}

func (t *SpeakTool) Name() string { return "speak" }

func (t *SpeakTool) Description() string {
	return "Speak text aloud on local speakers, or render an audio file (mode=file) to attach to a reply."
}

func (t *SpeakTool) Parameters() map[string]Param {
	return map[string]Param{
		"text": {Type: "string", Description: "Text to speak (max 4000 chars).", Required: true},
		"mode": {Type: "string", Description: "play (default) or file."},
	}
}

func (t *SpeakTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	text, _ := args["text"].(string)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("speak: missing required argument %q", "text")
	}
	mode, _ := args["mode"].(string)
	if strings.ToLower(strings.TrimSpace(mode)) == "file" {
		path, err := media.Synthesize(ctx, text, media.TTSConfig{})
		if err != nil {
			return "", err
		}
		return "audio saved to " + path, nil
	}
	if err := media.Speak(ctx, text); err != nil {
		return "", err
	}
	return "spoken", nil
}
