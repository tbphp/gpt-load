import eslintConfigPrettier from 'eslint-config-prettier'
import { globalIgnores } from 'eslint/config'
import pluginVue from 'eslint-plugin-vue'
import { defineConfigWithVueTs, vueTsConfigs } from '@vue/eslint-config-typescript'

export default defineConfigWithVueTs(
  globalIgnores(['node_modules/**', 'coverage/**']),
  pluginVue.configs['flat/recommended'],
  vueTsConfigs.recommended,
  eslintConfigPrettier,
)
