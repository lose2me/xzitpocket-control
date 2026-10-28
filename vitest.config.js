import { defineConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  resolve: { alias: { '/vendor': fileURLToPath(new URL('./web/vendor', import.meta.url)) } },
  test: {
    environment: 'jsdom',
    include: ['test/web/**/*.test.js'],
    coverage: {
      provider: 'v8',
      include: ['web/app.js', 'web/template.js'],
      reporter: ['text', 'lcov', 'json'],
      reportsDirectory: 'coverage/web',
      thresholds: { lines: 85, statements: 85, functions: 85, branches: 85 },
    },
  },
});
