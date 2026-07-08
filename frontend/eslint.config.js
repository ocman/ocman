import js from '@eslint/js'
import globals from 'globals'
import react from 'eslint-plugin-react'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores([
    'dist',
    'src/wailsjs/**', // Wails-generated bindings/runtime.
  ]),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    plugins: {
      react,
    },
    settings: {
      react: { version: 'detect' },
    },
    rules: {
      'react/no-unstable-nested-components': 'warn',
      'react/jsx-no-constructed-context-values': 'warn',
      // Complexity guardrails: warn-only so existing large components
      // surface as pressure without breaking CI. Thresholds sit above
      // the p90 of the codebase so only genuine outliers flag.
      complexity: ['warn', 20],
      'max-depth': ['warn', 4],
      'max-lines-per-function': [
        'warn',
        { max: 150, skipBlankLines: true, skipComments: true },
      ],
    },
  },
  {
    // Test files legitimately have long describe/it bodies and dense
    // assertion logic; the size/complexity budget is about production
    // components, not test scaffolding. Correctness rules still apply.
    files: ['**/*.{test,spec}.{ts,tsx}', 'e2e/**/*.{ts,tsx}'],
    rules: {
      complexity: 'off',
      'max-lines-per-function': 'off',
    },
  },
])
