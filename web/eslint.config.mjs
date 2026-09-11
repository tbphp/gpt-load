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
  {
    files: ['src/frontends/modern/**/*.vue'],
    rules: { 'vue/no-static-inline-styles': 'error' },
  },
  {
    files: ['src/frontends/modern/**/*.{ts,vue}'],
    ignores: ['src/frontends/modern/app/query.ts'],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector:
            'Property[key.name=/^(refetchInterval|refetchIntervalInBackground|refetchOnWindowFocus|refetchOnReconnect|refetchOnMount)$/]',
          message: '新版禁止定时轮询；刷新策略统一由 app/query.ts 管理，不在页面覆盖。',
        },
        {
          selector:
            'Property[key.value=/^(refetchInterval|refetchIntervalInBackground|refetchOnWindowFocus|refetchOnReconnect|refetchOnMount)$/]',
          message: '新版禁止定时轮询；刷新策略统一由 app/query.ts 管理，不在页面覆盖。',
        },
      ],
    },
  },
  {
    files: ['src/frontends/modern/{features,layouts}/**/*.vue'],
    rules: {
      'no-restricted-imports': [
        'error',
        {
          paths: [
            {
              name: 'reka-ui',
              importNames: [
                'DialogContent',
                'DialogOverlay',
                'DialogPortal',
                'DialogTitle',
                'DialogDescription',
                'DropdownMenuContent',
                'DropdownMenuPortal',
                'DropdownMenuRadioItem',
                'TooltipRoot',
                'TooltipContent',
              ],
              message: '复用 modern/components 中的公共浮层、菜单和提示组件。',
            },
          ],
        },
      ],
    },
  },
)
