// Compile JSX tests without a second application build or a browser dependency.
// Temporary output is beneath node_modules so external package imports resolve
// normally. Only this runner's freshly allocated directory is removed.
import { build } from 'esbuild'
import { mkdtemp, rm } from 'node:fs/promises'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

const root = fileURLToPath(new URL('../', import.meta.url))
const temp = await mkdtemp(join(root, 'node_modules', '.diffmind-component-tests-'))
try {
  const output = join(temp, 'tokens.test.mjs')
  await build({
    entryPoints: [join(root, 'src/views/ProjectTokens.test.jsx')], outfile: output,
    bundle: true, platform: 'node', format: 'esm', packages: 'external',
    jsx: 'automatic', jsxImportSource: 'preact', logLevel: 'warning',
  })
  const result = spawnSync(process.execPath, ['--test', output], { stdio: 'inherit' })
  if (result.error) throw result.error
  process.exitCode = result.status ?? 1
} finally {
  await rm(temp, { recursive: true, force: true })
}
