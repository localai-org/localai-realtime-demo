package scaffold

// Fixed pipeline stages. The scaffolder only lets the user swap the chat LLM
// and the TTS voice; the VAD and transcription models are the ones the realtime
// pipeline (gpt-realtime.yaml) is built around, so they're baked in. They lead
// the gallery-install command list, in this order.
const (
	VADModel           = "silero-vad-ggml"
	TranscriptionModel = "parakeet-cpp-tdt-0.6b-v3"
)

// streamingBackends are the LocalAI TTS backends that synthesize incrementally,
// so a streaming pipeline over them lowers time-to-first-audio. A model's
// Streaming flag is derived from this set rather than hand-set per row, so the
// annotation can't drift from reality.
var streamingBackends = map[string]bool{
	"sherpa-onnx":   true,
	"qwen3-tts-cpp": true,
	"voxcpm":        true,
	"vibevoice-cpp": true,
	"omnivoice-cpp": true,
}

// Model is a curated pick the user can choose without consulting the gallery.
// Streaming is computed from Backend (see streamingBackends), not stored.
type Model struct {
	ID          string
	Backend     string
	Description string
	Default     bool // the recommended pick, pre-selected at the prompt
}

// Streaming reports whether this model's backend emits audio incrementally.
func (m Model) Streaming() bool { return streamingBackends[m.Backend] }

// preferredLLMs is the curated chat-LLM shortlist. gemma-4-e2b is the default:
// it honors reasoning_effort:none (answers with no <think> preamble) and is
// fast on CPU/GPU.
var preferredLLMs = []Model{
	{ID: "gemma-4-e2b-it-qat-q4_0", Backend: "llama-cpp", Description: "fast on CPU/GPU, reasoning_effort:none", Default: true},
	{ID: "lfm2.5-8b-a1b", Backend: "llama-cpp", Description: "larger MoE, stronger answers, slower"},
}

// preferredTTS is the curated voice shortlist — all streaming-capable. The
// sherpa-onnx Paola voice is the default (fast Italian voice used by the
// wingman stack); kokoros / kokoro-multi-lang cover English and multilingual,
// qwen3-tts-cpp is the heavier neural option.
var preferredTTS = []Model{
	{ID: "vits-piper-it_IT-paola-sherpa", Backend: "sherpa-onnx", Description: "Italian (Paola)", Default: true},
	{ID: "kokoros", Backend: "sherpa-onnx", Description: "English (Kokoro)"},
	{ID: "qwen3-tts-cpp", Backend: "qwen3-tts-cpp", Description: "neural; speakers / voice-design"},
	{ID: "kokoro-multi-lang-v1.0-sherpa", Backend: "sherpa-onnx", Description: "multilingual (Kokoro)"},
}

// PreferredLLMs returns the curated chat-LLM shortlist (a copy).
func PreferredLLMs() []Model { return cloneModels(preferredLLMs) }

// PreferredTTS returns the curated TTS-voice shortlist (a copy).
func PreferredTTS() []Model { return cloneModels(preferredTTS) }

func cloneModels(in []Model) []Model {
	out := make([]Model, len(in))
	copy(out, in)
	return out
}
