import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist', 'coverage']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      globals: globals.browser,
    },
  },
  {
    // The polling hooks start their first fetch from an effect, which is what
    // `set-state-in-effect` is designed to flag. The rule's own remedy is to
    // move data fetching into a framework or a data layer such as React Query;
    // until FundKit grows one, an effect that owns a polling interval is the
    // honest implementation. Scoping the exemption to these hooks beats
    // scattering disable comments, and keeps the rule live everywhere else.
    files: ['src/hooks/use*.ts'],
    rules: {
      'react-hooks/set-state-in-effect': 'off',
    },
  },
  {
    // Test files run in Node under Vitest and legitimately use both globals.
    files: ['src/**/*.{test,spec}.{ts,tsx}', 'src/test/**/*.ts'],
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
    },
  },
])
