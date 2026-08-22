import ai2Logo from '@lobehub/icons-static-svg/icons/ai2-color.svg';
import anthropicLogo from '@lobehub/icons-static-svg/icons/anthropic.svg';
import awsLogo from '@lobehub/icons-static-svg/icons/aws-color.svg';
import cerebrasLogo from '@lobehub/icons-static-svg/icons/cerebras-color.svg';
import chatGLMLogo from '@lobehub/icons-static-svg/icons/chatglm-color.svg';
import claudeLogo from '@lobehub/icons-static-svg/icons/claude-color.svg';
import cohereLogo from '@lobehub/icons-static-svg/icons/cohere-color.svg';
import commandALogo from '@lobehub/icons-static-svg/icons/commanda-color.svg';
import codexLogo from '@lobehub/icons-static-svg/icons/codex-color.svg';
import dalleLogo from '@lobehub/icons-static-svg/icons/dalle-color.svg';
import dbrxLogo from '@lobehub/icons-static-svg/icons/dbrx-color.svg';
import deepSeekLogo from '@lobehub/icons-static-svg/icons/deepseek-color.svg';
import dolphinLogo from '@lobehub/icons-static-svg/icons/dolphin.svg';
import fluxLogo from '@lobehub/icons-static-svg/icons/flux.svg';
import geminiLogo from '@lobehub/icons-static-svg/icons/gemini-color.svg';
import gemmaLogo from '@lobehub/icons-static-svg/icons/gemma-color.svg';
import googleLogo from '@lobehub/icons-static-svg/icons/google-color.svg';
import groqLogo from '@lobehub/icons-static-svg/icons/groq.svg';
import grokLogo from '@lobehub/icons-static-svg/icons/grok.svg';
import kimiLogo from '@lobehub/icons-static-svg/icons/kimi-color.svg';
import llavaLogo from '@lobehub/icons-static-svg/icons/llava-color.svg';
import llmLogo from '@lobehub/icons-static-svg/icons/llmapi.svg';
import metaLogo from '@lobehub/icons-static-svg/icons/meta-color.svg';
import microsoftLogo from '@lobehub/icons-static-svg/icons/microsoft-color.svg';
import minimaxLogo from '@lobehub/icons-static-svg/icons/minimax-color.svg';
import mistralLogo from '@lobehub/icons-static-svg/icons/mistral-color.svg';
import nanoBananaLogo from '@lobehub/icons-static-svg/icons/nanobanana-color.svg';
import nousLogo from '@lobehub/icons-static-svg/icons/nousresearch.svg';
import novaLogo from '@lobehub/icons-static-svg/icons/nova-color.svg';
import nvidiaLogo from '@lobehub/icons-static-svg/icons/nvidia-color.svg';
import openAILogo from '@lobehub/icons-static-svg/icons/openai.svg';
import openRouterLogo from '@lobehub/icons-static-svg/icons/openrouter-color.svg';
import perplexityLogo from '@lobehub/icons-static-svg/icons/perplexity-color.svg';
import qwenLogo from '@lobehub/icons-static-svg/icons/qwen-color.svg';
import soraLogo from '@lobehub/icons-static-svg/icons/sora-color.svg';
import sunoLogo from '@lobehub/icons-static-svg/icons/suno.svg';
import togetherLogo from '@lobehub/icons-static-svg/icons/together-color.svg';
import xAILogo from '@lobehub/icons-static-svg/icons/xai.svg';
import yiLogo from '@lobehub/icons-static-svg/icons/yi-color.svg';
import zaiLogo from '@lobehub/icons-static-svg/icons/zai.svg';

export interface ModelLogoDefinition {
  name: string;
  src: string;
  patterns: RegExp[];
  color?: boolean;
}

// Model-family rules come before gateway rules so aliases keep their lab logo.
export const MODEL_LOGOS: ModelLogoDefinition[] = [
  { name: 'Claude', src: claudeLogo, color: true, patterns: [/(?:^|[/_.-])(?:claude|opus|sonnet|haiku|fable)(?=$|[/_.:-]|\d)/i] },
  { name: 'Codex', src: codexLogo, color: true, patterns: [/(?:^|[/_.-])codex(?=$|[/_.:-]|\d)/i] },
  { name: 'Gemini', src: geminiLogo, color: true, patterns: [/(?:^|[/_.-])gemini(?=$|[/_.:-]|\d)/i] },
  { name: 'Gemma', src: gemmaLogo, color: true, patterns: [/(?:^|[/_.-])gemma(?=$|[/_.:-]|\d)/i] },
  { name: 'Grok', src: grokLogo, patterns: [/(?:^|[/_.-])grok(?=$|[/_.:-]|\d)/i] },
  { name: 'Sora', src: soraLogo, color: true, patterns: [/(?:^|[/_.-])sora(?=$|[/_.:-]|\d)/i] },
  { name: 'DALL-E', src: dalleLogo, color: true, patterns: [/(?:^|[/_.-])dall[-_.]?e(?=$|[/_.:-]|\d)/i] },
  { name: 'Nova', src: novaLogo, color: true, patterns: [/(?:^|[/_.-])nova(?=$|[/_.:-]|\d)/i] },
  { name: 'DBRX', src: dbrxLogo, color: true, patterns: [/(?:^|[/_.-])dbrx(?=$|[/_.:-]|\d)/i] },
  { name: 'Command A', src: commandALogo, color: true, patterns: [/(?:^|[/_.-])command[-_.]?a(?=$|[/_.:-]|\d)/i] },
  { name: 'ChatGLM', src: chatGLMLogo, color: true, patterns: [/(?:^|[/_.-])chatglm(?=$|[/_.:-]|\d)/i] },
  { name: 'Nano Banana', src: nanoBananaLogo, color: true, patterns: [/(?:^|[/_.-])nano[-_.]?banana(?=$|[/_.:-]|\d)/i] },
  { name: 'LLaVA', src: llavaLogo, color: true, patterns: [/(?:^|[/_.-])llava(?=$|[/_.:-]|\d)/i] },
  { name: 'Flux', src: fluxLogo, patterns: [/(?:^|[/_.-])flux(?=$|[/_.:-]|\d)/i] },
  { name: 'Dolphin', src: dolphinLogo, patterns: [/(?:^|[/_.-])dolphin(?=$|[/_.:-]|\d)/i] },
  { name: 'Suno', src: sunoLogo, patterns: [/(?:^|[/_.-])suno(?=$|[/_.:-]|\d)/i] },
];

export const LAB_LOGOS: ModelLogoDefinition[] = [
  { name: 'Anthropic', src: anthropicLogo, patterns: [/(?:^|[/_.-])anthropic(?=$|[/_.:-]|\d)/i] },
  { name: 'OpenAI', src: openAILogo, patterns: [/(?:^|[/_.-])(?:openai|chatgpt|gpt|codex)(?=$|[/_.:-]|\d)/i, /(?:^|[/_.-])o[134](?=$|[/_.:-]|\d)/i] },
  { name: 'Google', src: googleLogo, color: true, patterns: [/(?:^|[/_.-])google(?=$|[/_.:-]|\d)/i] },
  { name: 'xAI', src: xAILogo, patterns: [/(?:^|[/_.-])(?:xai|grok)(?=$|[/_.:-]|\d)/i] },
  { name: 'Meta', src: metaLogo, color: true, patterns: [/(?:^|[/_.-])(?:meta|llama)(?=$|[/_.:-]|\d)/i] },
  { name: 'Mistral AI', src: mistralLogo, color: true, patterns: [/(?:^|[/_.-])(?:mistral|mixtral|codestral|ministral)(?=$|[/_.:-]|\d)/i] },
  { name: 'DeepSeek', src: deepSeekLogo, color: true, patterns: [/(?:^|[/_.-])deepseek(?=$|[/_.:-]|\d)/i] },
  { name: 'Qwen', src: qwenLogo, color: true, patterns: [/(?:^|[/_.-])(?:qwen|qwq|alibaba)(?=$|[/_.:-]|\d)/i] },
  { name: 'Z.ai', src: zaiLogo, patterns: [/(?:^|[/_.-])(?:zai|zhipu|chatglm|glm)(?=$|[/_.:-]|\d)/i] },
  { name: 'Moonshot AI', src: kimiLogo, color: true, patterns: [/(?:^|[/_.-])(?:moonshot|kimi)(?=$|[/_.:-]|\d)/i] },
  { name: 'MiniMax', src: minimaxLogo, color: true, patterns: [/(?:^|[/_.-])minimax(?=$|[/_.:-]|\d)/i] },
  { name: 'NVIDIA', src: nvidiaLogo, color: true, patterns: [/(?:^|[/_.-])(?:nvidia|nemotron)(?=$|[/_.:-]|\d)/i] },
  { name: 'Cohere', src: cohereLogo, color: true, patterns: [/(?:^|[/_.-])cohere(?=$|[/_.:-]|\d)/i, /(?:^|[/_.-])command[-_.]?[ar](?=$|[/_.:-]|\d)/i] },
  { name: 'Amazon', src: awsLogo, color: true, patterns: [/(?:^|[/_.-])(?:amazon|aws|bedrock)(?=$|[/_.:-]|\d)/i, /(?:^|[/_.-])nova(?=$|[/_.:-]|\d)/i] },
  { name: 'Microsoft', src: microsoftLogo, color: true, patterns: [/(?:^|[/_.-])(?:microsoft|azure|phi)(?=$|[/_.:-]|\d)/i] },
  { name: 'Perplexity', src: perplexityLogo, color: true, patterns: [/(?:^|[/_.-])(?:perplexity|sonar)(?=$|[/_.:-]|\d)/i] },
  { name: 'AI2', src: ai2Logo, color: true, patterns: [/(?:^|[/_.-])(?:allenai|olmo)(?=$|[/_.:-]|\d)/i] },
  { name: 'Nous Research', src: nousLogo, patterns: [/(?:^|[/_.-])(?:nous|hermes)(?=$|[/_.:-]|\d)/i] },
  { name: '01.AI', src: yiLogo, color: true, patterns: [/(?:^|[/_.-])(?:01[-_.]?ai|yi)(?=$|[/_.:-]|\d)/i] },
  { name: 'Cerebras', src: cerebrasLogo, color: true, patterns: [/(?:^|[/_.-])cerebras(?=$|[/_.:-]|\d)/i] },
  { name: 'Groq', src: groqLogo, patterns: [/^groq$/i] },
  { name: 'Together AI', src: togetherLogo, color: true, patterns: [/^together(?:ai)?$/i] },
  { name: 'OpenRouter', src: openRouterLogo, color: true, patterns: [/^openrouter$/i] },
];

const UNKNOWN_MODEL: ModelLogoDefinition = { name: 'Unknown model', src: llmLogo, patterns: [] };

export function resolveModelLogo(model: string): ModelLogoDefinition {
  const slash = model.indexOf('/');
  const provider = slash >= 0 ? model.slice(0, slash) : '';
  const modelID = slash >= 0 ? model.slice(slash + 1) : model;
  const matches = (logos: ModelLogoDefinition[], value: string) => logos.find((logo) => logo.patterns.some((pattern) => pattern.test(value)));
  return matches(MODEL_LOGOS, modelID) ?? matches(LAB_LOGOS, modelID) ?? matches(LAB_LOGOS, provider) ?? UNKNOWN_MODEL;
}
