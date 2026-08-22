import { describe, expect, it } from 'vitest';
import { LAB_LOGOS, MODEL_LOGOS, resolveModelLogo } from './modelLogos';

const ICON_TITLES: Record<string, string> = {
  'Claude': 'Claude',
  'Codex': 'Codex',
  'Gemini': 'Gemini',
  'Gemma': 'Gemma',
  'Grok': 'Grok',
  'Sora': 'Sora',
  'DALL-E': 'DALL-E',
  'Nova': 'Nova',
  'DBRX': 'DBRX',
  'Command A': 'CommandA',
  'ChatGLM': 'ChatGLM',
  'Nano Banana': 'NanoBanana',
  'LLaVA': 'LLaVA',
  'Flux': 'Flux',
  'Dolphin': 'Dolphin',
  'Suno': 'Suno',
  'Anthropic': 'Anthropic',
  'OpenAI': 'OpenAI',
  'Google': 'Google',
  'xAI': 'Grok',
  'Meta': 'Meta',
  'Mistral AI': 'Mistral',
  'DeepSeek': 'DeepSeek',
  'Qwen': 'Qwen',
  'Z.ai': 'Z.ai',
  'Moonshot AI': 'Kimi',
  'MiniMax': 'Minimax',
  'NVIDIA': 'Nvidia',
  'Cohere': 'Cohere',
  'Amazon': 'AWS',
  'Microsoft': 'Azure',
  'Perplexity': 'Perplexity',
  'AI2': 'Ai2',
  'Nous Research': 'NousResearch',
  '01.AI': 'Yi',
  'Cerebras': 'Cerebras',
  'Groq': 'Groq',
  'Together AI': 'together.ai',
  'OpenRouter': 'OpenRouter',
};

describe('resolveModelLogo', () => {
  it.each([
    ['opencode/claude-opus-4-8', 'Claude', 'claude.svg'],
    ['github-copilot/GPT-5.1-codex', 'Codex', 'codex.svg'],
    ['google/gemini-3-pro', 'Gemini', 'gemini.svg'],
    ['lmstudio/google/gemma-4-e4b', 'Gemma', 'gemma.svg'],
    ['xai/grok-4', 'Grok', 'grok.svg'],
    ['openai/sora-2', 'Sora', 'sora-color.svg'],
    ['openai/dall-e-3', 'DALL-E', 'dalle.svg'],
    ['bedrock/nova-pro', 'Nova', 'nova.svg'],
    ['databricks/dbrx-instruct', 'DBRX', 'dbrx.svg'],
    ['cohere/command-a-03-2025', 'Command A', 'commanda.svg'],
    ['zhipu/chatglm-6b', 'ChatGLM', 'chatglm.svg'],
    ['google/nano-banana-pro', 'Nano Banana', 'nanobanana.svg'],
    ['openrouter/llava-1.6', 'LLaVA', 'llava-color.svg'],
    ['black-forest-labs/flux-1', 'Flux', 'flux.svg'],
    ['openrouter/dolphin-3', 'Dolphin', 'dolphin.svg'],
    ['suno/suno-v4', 'Suno', 'suno.svg'],
  ])('maps dedicated model %s to %s', (model, name, asset) => {
    const logo = resolveModelLogo(model);
    expect(logo.name).toBe(name);
    expect(logo.src.includes(asset) || decodeURIComponent(logo.src).includes(`<title>${ICON_TITLES[name]}</title>`)).toBe(true);
  });

  it.each([
    ['anthropic/custom-model', 'Anthropic', 'anthropic.svg'],
    ['openai/custom-model', 'OpenAI', 'openai.svg'],
    ['google/custom-model', 'Google', 'google.svg'],
    ['xai/custom-model', 'xAI', 'xai.svg'],
    ['ollama/llama3.3', 'Meta', 'meta-color.svg'],
    ['openrouter/mistral-large', 'Mistral AI', 'mistral.svg'],
    ['openrouter/deepseek-r1', 'DeepSeek', 'deepseek.svg'],
    ['ollama/qwen3:8b', 'Qwen', 'qwen.svg'],
    ['opencode/glm-5.1', 'Z.ai', 'zai.svg'],
    ['opencode/kimi-k2.5-free', 'Moonshot AI', 'kimi.svg'],
    ['opencode/minimax-m2.5-free', 'MiniMax', 'minimax.svg'],
    ['opencode/nemotron-3-super-free', 'NVIDIA', 'nvidia.svg'],
    ['cohere/command-r-plus', 'Cohere', 'cohere.svg'],
    ['bedrock/custom-model', 'Amazon', 'aws.svg'],
    ['azure/phi-4', 'Microsoft', 'microsoft.svg'],
    ['perplexity/sonar-pro', 'Perplexity', 'perplexity.svg'],
    ['openrouter/olmo-2', 'AI2', 'ai2.svg'],
    ['openrouter/hermes-4', 'Nous Research', 'nousresearch.svg'],
    ['openrouter/yi-large', '01.AI', 'yi.svg'],
    ['cerebras/custom-model', 'Cerebras', 'cerebras.svg'],
    ['groq/custom-model', 'Groq', 'groq.svg'],
    ['togetherai/custom-model', 'Together AI', 'together.svg'],
    ['openrouter/custom-model', 'OpenRouter', 'openrouter.svg'],
  ])('falls back from %s to %s', (model, lab, asset) => {
    const logo = resolveModelLogo(model);
    expect(logo.name).toBe(lab);
    expect(logo.src.includes(asset) || decodeURIComponent(logo.src).includes(`<title>${ICON_TITLES[lab]}</title>`)).toBe(true);
  });

  it.each([
    ['google-vertex/llama-3', 'Meta'],
    ['openai-compatible/deepseek_r1', 'DeepSeek'],
    ['openrouter/claude_opus', 'Claude'],
    ['openrouter/gpt_4o', 'OpenAI'],
  ])('prefers the model lab for %s', (model, lab) => {
    expect(resolveModelLogo(model).name).toBe(lab);
  });

  it('matches a bare model ID', () => {
    expect(resolveModelLogo('claude-opus-4').name).toBe('Claude');
  });

  it('returns a generic logo for an unknown model', () => {
    const logo = resolveModelLogo('local/new-model');
    expect(logo.name).toBe('Unknown model');
    expect(decodeURIComponent(logo.src)).toContain('<title>LLM API</title>');
  });

  it('has a tested case for every supported logo', () => {
    expect(LAB_LOGOS).toHaveLength(23);
    expect(MODEL_LOGOS).toHaveLength(16);
  });
});
