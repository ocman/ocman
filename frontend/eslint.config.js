import js from '@eslint/js'
import globals from 'globals'
import react from 'eslint-plugin-react'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

// The shared control components: the only places that render raw
// <button>/<input>/<select>/<textarea>.
const sharedControlFiles = [
  'src/components/Control.tsx',
  'src/components/IconButton.tsx',
  'src/components/CopyButton.tsx',
  'src/components/CheckboxField.tsx',
  'src/components/ToggleField.tsx',
  'src/components/SegmentedControl.tsx',
  'src/components/SettingRow.tsx',
  'src/components/DataTable.tsx',
]

// ponytail: legacy offenders grandfathered in. Shrink this list as files
// move to the shared controls; never add to it.
const rawControlAllowlist = [
  'src/components/AppHeader.tsx',
  'src/components/ArchiveButton.tsx',
  'src/components/assistant/ApprovalFootnote.tsx',
  'src/components/assistant/CommandListPicker.tsx',
  'src/components/assistant/Composer.tsx',
  'src/components/assistant/ComposerChrome.tsx',
  'src/components/assistant/MarkdownText.tsx',
  'src/components/assistant/ModelPicker.tsx',
  'src/components/assistant/QueuedMessages.tsx',
  'src/components/assistant/ToolCallDisplay.tsx',
  'src/components/AssistantThread.tsx',
  'src/components/ChangedFileRow.tsx',
  'src/components/CommandPalette.tsx',
  'src/components/FactoryActionCard.tsx',
  'src/components/FactoryControlCard.tsx',
  'src/components/FactoryPlanGraph.tsx',
  'src/components/FileChangeGroup.tsx',
  'src/components/LaunchProgressCard.tsx',
  'src/components/MainNav.tsx',
  'src/components/MessageBookmarksPane.tsx',
  'src/components/PermissionModeLock.tsx',
  'src/components/PermissionRulesEditor.tsx',
  'src/components/PluginActionDialog.tsx',
  'src/components/RightPanel.tsx',
  'src/components/RightPanelPane.tsx',
  'src/components/SearchSelect.tsx',
  'src/components/session/PermissionPrompt.tsx',
  'src/components/session/QuestionPrompt.tsx',
  'src/components/SessionInfoSections.tsx',
  'src/components/SessionTable.tsx',
  'src/components/SessionTerminalDock.tsx',
  'src/components/SettingsSearch.tsx',
  'src/components/TerminalPane.tsx',
  'src/components/upstream/ExpandableRow.tsx',
  'src/components/upstream/LaunchSplitButton.tsx',
  'src/components/upstream/RemoteErrorBanner.tsx',
  'src/pages/dashboard/AnalyticsTab.tsx',
  'src/pages/FactoryConfiguration.tsx',
  'src/pages/FactoryEpics.tsx',
  'src/pages/FactoryLayout.tsx',
  'src/pages/FactoryOverview.tsx',
  'src/pages/ImportSharedConversation.tsx',
  'src/pages/Inbox.tsx',
  'src/pages/session-detail/MobileHeaderControls.tsx',
  'src/pages/session-detail/MovePicker.tsx',
  'src/pages/session-detail/RenameModal.tsx',
  'src/pages/session-detail/SessionToasts.tsx',
  'src/pages/session-detail/SidebarHeader.tsx',
  'src/pages/session-detail/SidebarSessionRow.tsx',
  'src/pages/session-detail/SseStatusIndicator.tsx',
  'src/pages/SharedConversationView.tsx',
]

export default defineConfig([
  globalIgnores([
    'dist',
    'perf/dist*', // `pnpm perf` profiling builds.
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
    // Raw form controls bypass the shared components (Button, IconButton,
    // TextField, SelectField, …) and drift off-theme. The shared control
    // components themselves and the legacy offenders below are exempt.
    files: ['src/**/*.tsx'],
    ignores: [
      '**/*.{test,spec}.tsx',
      ...sharedControlFiles,
      ...rawControlAllowlist,
    ],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector: "JSXOpeningElement[name.name=/^(button|input|select|textarea)$/]",
          message:
            'Use the shared controls (Button, IconButton, TextField, SelectField, TextareaField, CheckboxField from components/Control.tsx) instead of a raw form element.',
        },
      ],
    },
  },
  {
    // Test files legitimately have long describe/it bodies and dense
    // assertion logic; the size/complexity budget is about production
    // components, not test scaffolding. Correctness rules still apply.
    files: ['**/*.{test,spec}.{ts,tsx}', 'e2e/**/*.{ts,tsx}', 'perf/**/*.ts'],
    rules: {
      complexity: 'off',
      'max-lines-per-function': 'off',
    },
  },
])
