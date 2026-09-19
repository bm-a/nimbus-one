package main

import (
	"fmt"
	"strings"
)

// Provider families (wire shapes). Mirrors OpenClaw's `api` field:
// nearly everything is OpenAI-compatible; a few speak Anthropic's
// /v1/messages shape natively.
const (
	familyAnthropic = "anthropic" // POST {base}/v1/messages
	familyOpenAI    = "openai"    // POST {base}/chat/completions
)

// Provider is one model source. The table is DATA (mirroring OpenClaw's
// modelCatalog: ids, base URLs, key envs, and default models all come
// from extensions/*/openclaw.plugin.json), so adding a provider is a
// row, not a client. Two clients cover every row (anthropic.go,
// openai.go).
type Provider struct {
	ID           string   // stable id used in config/CLI
	Name         string   // display name
	Base         string   // API base URL (no trailing path)
	Family       string   // familyAnthropic | familyOpenAI
	KeyEnvs      []string // env vars holding the key, in order (empty = local, no key)
	DefaultModel string   // first-listed OpenClaw catalog model ("" = model required)
	Local        bool     // self-hosted; no key, localhost base
	Note         string   // one-line caveat shown in `models`
}

// providers is the full OpenClaw-parity table (key-based + local).
// Deliberately EXCLUDED (documented in DECISIONS.md): cloud SDK-auth
// (bedrock, vertex, azure — no plain API keys), OAuth flows
// (claude-cli, codex, copilot, minimax), entries with no usable base
// (qwen main, alibaba — ambiguous endpoints), ollama-cloud (compat
// path unverifiable from the manifest).
var providers = []Provider{
	// Native Anthropic shape.
	{ID: "anthropic", Name: "Anthropic", Base: "https://api.anthropic.com", Family: familyAnthropic, KeyEnvs: []string{"ANTHROPIC_API_KEY"}, DefaultModel: "claude-sonnet-4-5-20250929", Note: "default; native /v1/messages"},
	{ID: "kimi", Name: "Kimi", Base: "https://api.kimi.com/coding", Family: familyAnthropic, KeyEnvs: []string{"KIMICODE_API_KEY", "KIMI_API_KEY"}, DefaultModel: "kimi-for-coding", Note: "Anthropic-compatible endpoint"},

	// OpenAI-compatible shape, cloud (key required).
	{ID: "openai", Name: "OpenAI", Base: "https://api.openai.com/v1", Family: familyOpenAI, KeyEnvs: []string{"OPENAI_API_KEY"}, DefaultModel: "gpt-6-astra"},
	{ID: "openrouter", Name: "OpenRouter", Base: "https://openrouter.ai/api/v1", Family: familyOpenAI, KeyEnvs: []string{"OPENROUTER_API_KEY"}, DefaultModel: "", Note: "model required (hundreds available)"},
	{ID: "deepseek", Name: "DeepSeek", Base: "https://api.deepseek.com", Family: familyOpenAI, KeyEnvs: []string{"DEEPSEEK_API_KEY"}, DefaultModel: "deepseek-v4-flash"},
	{ID: "groq", Name: "Groq", Base: "https://api.groq.com/openai/v1", Family: familyOpenAI, KeyEnvs: []string{"GROQ_API_KEY"}, DefaultModel: "groq/compound"},
	{ID: "mistral", Name: "Mistral", Base: "https://api.mistral.ai/v1", Family: familyOpenAI, KeyEnvs: []string{"MISTRAL_API_KEY"}, DefaultModel: "codestral-latest"},
	{ID: "xai", Name: "xAI", Base: "https://api.x.ai/v1", Family: familyOpenAI, KeyEnvs: []string{"XAI_API_KEY"}, DefaultModel: "grok-4.6"},
	{ID: "google", Name: "Google Gemini", Base: "https://generativelanguage.googleapis.com/v1beta/openai", Family: familyOpenAI, KeyEnvs: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, DefaultModel: "gemini-2.5-pro", Note: "via Google's OpenAI-compat endpoint"},
	{ID: "together", Name: "Together", Base: "https://api.together.xyz/v1", Family: familyOpenAI, KeyEnvs: []string{"TOGETHER_API_KEY"}, DefaultModel: "moonshotai/Kimi-K2.6"},
	{ID: "fireworks", Name: "Fireworks", Base: "https://api.fireworks.ai/inference/v1", Family: familyOpenAI, KeyEnvs: []string{"FIREWORKS_API_KEY"}, DefaultModel: "accounts/fireworks/models/kimi-k2p6"},
	{ID: "cerebras", Name: "Cerebras", Base: "https://api.cerebras.ai/v1", Family: familyOpenAI, KeyEnvs: []string{"CEREBRAS_API_KEY"}, DefaultModel: "gpt-oss-120b"},
	{ID: "deepinfra", Name: "DeepInfra", Base: "https://api.deepinfra.com/v1/openai", Family: familyOpenAI, KeyEnvs: []string{"DEEPINFRA_API_KEY"}, DefaultModel: "deepseek-ai/DeepSeek-V4-Flash"},
	{ID: "novita", Name: "Novita", Base: "https://api.novita.ai/openai/v1", Family: familyOpenAI, KeyEnvs: []string{"NOVITA_API_KEY"}, DefaultModel: "moonshotai/kimi-k3"},
	{ID: "nvidia", Name: "NVIDIA NIM", Base: "https://integrate.api.nvidia.com/v1", Family: familyOpenAI, KeyEnvs: []string{"NVIDIA_API_KEY"}, DefaultModel: "nvidia/nemotron-3-super-120b-a12b"},
	{ID: "moonshot", Name: "Moonshot", Base: "https://api.moonshot.ai/v1", Family: familyOpenAI, KeyEnvs: []string{"MOONSHOT_API_KEY", "KIMI_API_KEY"}, DefaultModel: "kimi-k3"},
	{ID: "zai", Name: "Z.AI", Base: "https://api.z.ai/api/paas/v4", Family: familyOpenAI, KeyEnvs: []string{"ZAI_API_KEY", "Z_AI_API_KEY"}, DefaultModel: "glm-5.3"},
	{ID: "qianfan", Name: "Baidu Qianfan", Base: "https://qianfan.baidubce.com/v2", Family: familyOpenAI, KeyEnvs: []string{"QIANFAN_API_KEY"}, DefaultModel: "ernie-5.1"},
	{ID: "cohere", Name: "Cohere", Base: "https://api.cohere.ai/compatibility/v1", Family: familyOpenAI, KeyEnvs: []string{"COHERE_API_KEY"}, DefaultModel: "command-a-plus-05-2026"},
	{ID: "huggingface", Name: "Hugging Face", Base: "https://router.huggingface.co/v1", Family: familyOpenAI, KeyEnvs: []string{"HF_TOKEN", "HUGGINGFACE_HUB_TOKEN"}, DefaultModel: "deepseek-ai/DeepSeek-V3.1"},
	{ID: "meta", Name: "Meta", Base: "https://api.meta.ai/v1", Family: familyOpenAI, KeyEnvs: []string{"MODEL_API_KEY"}, DefaultModel: "muse-spark-1.3", Note: "live-test pending"},
	{ID: "opencode", Name: "Opencode Zen", Base: "https://opencode.ai/zen/v1", Family: familyOpenAI, KeyEnvs: []string{"OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY"}, DefaultModel: "claude-opus-5", Note: "live-test pending"},
	{ID: "opencode-go", Name: "Opencode Zen Go", Base: "https://opencode.ai/zen/go/v1", Family: familyOpenAI, KeyEnvs: []string{"OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY"}, DefaultModel: "deepseek-v4-pro", Note: "live-test pending"},
	{ID: "chutes", Name: "Chutes", Base: "https://llm.chutes.ai/v1", Family: familyOpenAI, KeyEnvs: []string{"CHUTES_API_KEY"}, DefaultModel: "deepseek-ai/DeepSeek-V3.2-TEE"},
	{ID: "baseten", Name: "Baseten", Base: "https://inference.baseten.co/v1", Family: familyOpenAI, KeyEnvs: []string{"BASETEN_API_KEY"}, DefaultModel: "deepseek-ai/DeepSeek-V4-Pro"},
	{ID: "kilocode", Name: "Kilocode", Base: "https://api.kilo.ai/api/gateway", Family: familyOpenAI, KeyEnvs: []string{"KILOCODE_API_KEY"}, DefaultModel: "kilo-auto/balanced"},
	{ID: "venice", Name: "Venice", Base: "https://api.venice.ai/api/v1", Family: familyOpenAI, KeyEnvs: []string{"VENICE_API_KEY"}, DefaultModel: "venice-uncensored-1-2"},
	{ID: "featherless", Name: "Featherless", Base: "https://api.featherless.ai/v1", Family: familyOpenAI, KeyEnvs: []string{"FEATHERLESS_API_KEY"}, DefaultModel: "Qwen/Qwen3-32B"},
	{ID: "gmi", Name: "GMI", Base: "https://api.gmi-serving.com/v1", Family: familyOpenAI, KeyEnvs: []string{"GMI_API_KEY"}, DefaultModel: "zai-org/GLM-5.2-FP8"},
	{ID: "longcat", Name: "LongCat", Base: "https://api.longcat.chat/openai", Family: familyOpenAI, KeyEnvs: []string{"LONGCAT_API_KEY"}, DefaultModel: "LongCat-2.0"},
	{ID: "arcee", Name: "Arcee", Base: "https://api.arcee.ai/api/v1", Family: familyOpenAI, KeyEnvs: []string{"ARCEEAI_API_KEY"}, DefaultModel: "", Note: "model required"},
	{ID: "synthetic", Name: "Synthetic", Base: "https://api.synthetic.new/openai/v1", Family: familyOpenAI, KeyEnvs: []string{"SYNTHETIC_API_KEY"}, DefaultModel: "", Note: "model required"},
	{ID: "vercel-ai-gateway", Name: "Vercel AI Gateway", Base: "https://ai-gateway.vercel.sh/v1", Family: familyOpenAI, KeyEnvs: []string{"AI_GATEWAY_API_KEY"}, DefaultModel: "", Note: "model required; path unverified in manifest — report breakage"},
	{ID: "byteplus", Name: "BytePlus Ark", Base: "https://ark.ap-southeast.bytepluses.com/api/v3", Family: familyOpenAI, KeyEnvs: []string{"BYTEPLUS_API_KEY"}, DefaultModel: "dola-seed-2-1-turbo-260628"},
	{ID: "byteplus-plan", Name: "BytePlus Ark Coding", Base: "https://ark.ap-southeast.bytepluses.com/api/coding/v3", Family: familyOpenAI, KeyEnvs: []string{"BYTEPLUS_API_KEY"}, DefaultModel: "ark-code-latest"},
	{ID: "volcengine", Name: "Volcengine Ark", Base: "https://ark.cn-beijing.volces.com/api/v3", Family: familyOpenAI, KeyEnvs: []string{"VOLCENGINE_API_KEY"}, DefaultModel: "doubao-seed-evolving"},
	{ID: "volcengine-plan", Name: "Volcengine Ark Coding", Base: "https://ark.cn-beijing.volces.com/api/coding/v3", Family: familyOpenAI, KeyEnvs: []string{"VOLCENGINE_API_KEY"}, DefaultModel: "ark-code-latest"},
	{ID: "stepfun", Name: "StepFun", Base: "https://api.stepfun.ai/v1", Family: familyOpenAI, KeyEnvs: []string{"STEPFUN_API_KEY"}, DefaultModel: "step-3.7-flash"},
	{ID: "stepfun-plan", Name: "StepFun Coding", Base: "https://api.stepfun.ai/step_plan/v1", Family: familyOpenAI, KeyEnvs: []string{"STEPFUN_API_KEY"}, DefaultModel: "step-3.7-flash"},
	{ID: "xiaomi", Name: "Xiaomi MiMo", Base: "https://api.xiaomimimo.com/v1", Family: familyOpenAI, KeyEnvs: []string{"XIAOMI_API_KEY"}, DefaultModel: "mimo-v2.5-pro"},
	{ID: "xiaomi-token-plan", Name: "Xiaomi Token Plan", Base: "https://token-plan-sgp.xiaomimimo.com/v1", Family: familyOpenAI, KeyEnvs: []string{"XIAOMI_API_KEY", "XIAOMI_TOKEN_PLAN_API_KEY"}, DefaultModel: "mimo-v2.5-pro"},
	{ID: "qwen-token-plan", Name: "Qwen Token Plan", Base: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1", Family: familyOpenAI, KeyEnvs: []string{"QWEN_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"}, DefaultModel: "qwen3.7-plus"},
	{ID: "tencent-tokenhub", Name: "Tencent TokenHub", Base: "https://tokenhub.tencentmaas.com/v1", Family: familyOpenAI, KeyEnvs: []string{"TOKENHUB_API_KEY"}, DefaultModel: "hy3"},
	{ID: "tencent-tokenplan", Name: "Tencent TokenPlan", Base: "https://api.lkeap.cloud.tencent.com/plan/v3", Family: familyOpenAI, KeyEnvs: []string{"TOKENPLAN_API_KEY"}, DefaultModel: "hy3"},

	// Local / self-hosted (no key, OpenAI-compatible).
	{ID: "ollama", Name: "Ollama (local)", Base: "http://127.0.0.1:11434/v1", Family: familyOpenAI, DefaultModel: "llama3.1", Local: true, Note: "needs `ollama serve` + a pulled model"},
	{ID: "lmstudio", Name: "LM Studio (local)", Base: "http://localhost:1234/v1", Family: familyOpenAI, DefaultModel: "", Local: true, Note: "model required (your loaded model)"},
	{ID: "llamacpp", Name: "llama.cpp server (local)", Base: "http://127.0.0.1:8080/v1", Family: familyOpenAI, DefaultModel: "", Local: true, Note: "model required (your loaded model)"},
}

// lookupProvider returns the table row for id (case-insensitive).
func lookupProvider(id string) (Provider, error) {
	for _, p := range providers {
		if strings.EqualFold(p.ID, strings.TrimSpace(id)) {
			return p, nil
		}
	}
	return Provider{}, fmt.Errorf("unknown provider %q — run `nimbus-min models` for the list", id)
}
