// Flat ESLint config (create-vue style). `vue/no-v-html` is an error: LLM and user text
// is never interpreted as HTML (docs/contracts/ui.md §6.2, REQ-UI-01).
import { vueTsConfigs, withVueTs } from '@vue/eslint-config-typescript'
import skipFormatting from 'eslint-config-prettier/flat'
import pluginVue from 'eslint-plugin-vue'
import globals from 'globals'

export default withVueTs(
  {
    name: 'scc/ignores',
    ignores: ['dist/**', 'coverage/**', 'node_modules/**', 'src/api/schema.gen.ts'],
  },
  { name: 'scc/files', files: ['**/*.{ts,mts,vue}'] },
  pluginVue.configs['flat/recommended'],
  vueTsConfigs.recommended,
  {
    name: 'scc/rules',
    languageOptions: { globals: { ...globals.browser } },
    rules: {
      'vue/no-v-html': 'error',
      'vue/multi-word-component-names': 'off',
      'no-console': ['error', { allow: ['warn', 'error'] }],
      'no-debugger': 'error',
    },
  },
  {
    // shadcn-vue CLI output: optional props are intentionally undefined by default.
    name: 'scc/shadcn-ui',
    files: ['src/components/ui/**/*.vue'],
    rules: { 'vue/require-default-prop': 'off' },
  },
  {
    name: 'scc/node-files',
    files: ['*.config.{js,ts}', 'scripts/**/*.mjs'],
    languageOptions: { globals: { ...globals.node } },
  },
  skipFormatting,
)
