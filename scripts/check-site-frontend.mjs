import { spawnSync } from 'node:child_process'
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const baseline = '7d8b91268dab69d7150f345c448b5e250163146a'
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const web = resolve(root, 'web')
const output = resolve(
  process.env.RUNNER_TEMP || tmpdir(),
  'site-frontend-checks'
)
mkdirSync(output, { recursive: true })

function git(args) {
  const result = spawnSync('git', args, { cwd: root })
  if (result.status !== 0) {
    throw new Error(result.stderr.toString() || `git ${args[0]} failed`)
  }
  return result.stdout
}

function check(name, command, args) {
  const result = spawnSync(command, args, {
    cwd: web,
    env: {
      ...process.env,
      PATH: `${resolve(web, 'node_modules/.bin')}:${process.env.PATH || ''}`,
    },
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  })
  writeFileSync(resolve(output, `${name}.log`), result.stdout || '')
  writeFileSync(resolve(output, `${name}.stderr.log`), result.stderr || '')
  process.stdout.write(result.stdout || '')
  process.stderr.write(result.stderr || '')
  if (result.error || result.signal || result.status === null) {
    throw result.error || new Error(`${name} terminated: ${result.signal}`)
  }
  return result
}

const failures = []
const summary = [
  `## Frontend quality checks`,
  '',
  `Fixed baseline: ${baseline}`,
  '',
]

try {
  git(['cat-file', '-e', `${baseline}^{commit}`])
  const changed = new Set(
    Buffer.concat([
      git([
        'diff',
        '--name-only',
        '-z',
        '--diff-filter=ACMRTUXB',
        baseline,
        '--',
        'web/',
      ]),
      git(['ls-files', '--others', '--exclude-standard', '-z', '--', 'web/']),
    ])
      .toString()
      .split('\0')
      .filter(Boolean)
      .map((file) => file.slice('web/'.length))
  )
  writeFileSync(
    resolve(output, 'changed-files.json'),
    JSON.stringify([...changed], null, 2)
  )

  const lint = check('lint', 'bun', ['run', 'lint', '--format', 'json'])
  const format = check('format', 'bun', ['run', 'format:check'])
  const report = JSON.parse(lint.stdout)
  if (
    !Array.isArray(report.diagnostics) ||
    !Number.isInteger(report.number_of_files) ||
    report.number_of_files === 0
  ) {
    throw new Error('Unrecognized or empty global lint report')
  }
  const errors = report.diagnostics.filter(
    (diagnostic) => diagnostic.severity === 'error'
  )
  if (lint.status !== (errors.length > 0 ? 1 : 0)) {
    throw new Error(`Unexpected global lint exit status: ${lint.status}`)
  }
  const lintFiles = new Map()
  for (const diagnostic of errors) {
    if (!diagnostic.filename) throw new Error('Lint error has no filename')
    lintFiles.set(
      diagnostic.filename,
      (lintFiles.get(diagnostic.filename) || 0) + 1
    )
  }

  const formatMarker = 'Format issues found in protected-header-safe check:\n'
  let formatFiles = []
  if (format.status !== 0) {
    const sections = format.stderr.split(formatMarker)
    if (format.status !== 1 || sections.length !== 2) {
      throw new Error(`Unexpected global format failure: ${format.status}`)
    }
    formatFiles = sections[1]
      .trim()
      .split('\n')
      .filter(
        (line) =>
          line && line !== 'error: script "format:check" exited with code 1'
      )
    if (formatFiles.length === 0) {
      throw new Error('Global format failure has no filenames')
    }
  }

  summary.push(
    `Global lint: ${errors.length} errors; global format: ${formatFiles.length} files.`,
    ''
  )
  for (const file of new Set([...lintFiles.keys(), ...formatFiles])) {
    const relative = `web/${file}`
    const original = spawnSync('git', ['show', `${baseline}:${relative}`], {
      cwd: root,
    })
    const unchanged =
      original.status === 0 &&
      original.stdout.equals(readFileSync(resolve(web, file)))
    const details = `${lintFiles.get(file) || 0} lint errors${formatFiles.includes(file) ? ', format issues' : ''}`
    if (changed.has(file) || !unchanged) {
      failures.push(`${relative}: ${details}; differs from fixed baseline`)
    } else {
      summary.push(
        `- Allowed existing issue: ${relative} (${details}); byte-identical to baseline.`
      )
    }
  }

  summary.push(
    '',
    `Strict checks cover ${changed.size} added/changed frontend files:`,
    ''
  )
  for (const file of changed) summary.push(`- web/${file}`)
  if (changed.size > 0) {
    const changedLint = check('changed-lint', 'bunx', [
      '--no-install',
      'oxlint',
      '-c',
      '.oxlintrc.json',
      '--no-ignore',
      '--no-error-on-unmatched-pattern',
      ...changed,
    ])
    const changedFormat = check('changed-format', 'bunx', [
      '--no-install',
      'oxfmt',
      '-c',
      '.oxfmtrc.json',
      '--check',
      ...changed,
    ])
    if (changedLint.status !== 0) failures.push('Changed-file lint failed')
    if (changedFormat.status !== 0) {
      failures.push('Changed-file formatting failed')
    }
  }
} catch (error) {
  failures.push(error.message)
} finally {
  summary.push(
    '',
    failures.length > 0 ? 'Result: FAILED' : 'Result: PASSED',
    ...failures.map((failure) => `- ${failure}`),
    ''
  )
  const markdown = summary.join('\n')
  writeFileSync(resolve(output, 'summary.md'), markdown)
  if (process.env.GITHUB_STEP_SUMMARY) {
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, markdown)
  }
  console.log(markdown)
}

process.exitCode = failures.length > 0 ? 1 : 0
