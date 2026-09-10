import eslintConfigPrettier from 'eslint-config-prettier'
import { globalIgnores } from 'eslint/config'
import pluginVue from 'eslint-plugin-vue'
import { defineConfigWithVueTs, vueTsConfigs } from '@vue/eslint-config-typescript'

import frontendBoundaries from './eslint/frontend-boundaries.mjs'

export default defineConfigWithVueTs(
  globalIgnores(['node_modules/**']),
  pluginVue.configs['flat/recommended'],
  vueTsConfigs.recommended,
  eslintConfigPrettier,
  {
    files: ['src/**/*.{ts,vue}'],
    plugins: { 'frontend-boundaries': { rules: { 'no-cross-imports': frontendBoundaries } } },
    rules: { 'frontend-boundaries/no-cross-imports': 'error' },
  },
)
