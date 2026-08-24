export const meta = {
  name: 'review-branch',
  description: 'Review the current branch across independent dimensions, then try to refute every finding before reporting it',
  whenToUse: 'before opening a PR, or when a review needs to be more than one pass of one model',
  phases: [
    { title: 'Scope', detail: 'read the diff once, cheaply' },
    { title: 'Review', detail: 'one agent per dimension, in parallel' },
    { title: 'Verify', detail: 'independent skeptics per finding' },
  ],
}

// Args: { base?: string, dimensions?: string[] }
const base = (args && args.base) || 'main'
const DIMENSIONS = (args && args.dimensions) || [
  'correctness bugs — logic that produces a wrong result, not style',
  'error handling — unchecked errors, swallowed failures, wrong fallbacks',
  'concurrency — data races, deadlocks, cancellation that leaks goroutines',
  'test coverage — behaviour the diff changes that no test exercises',
]

const FINDINGS = {
  type: 'object',
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          title: { type: 'string' },
          file: { type: 'string' },
          line: { type: 'integer' },
          why: { type: 'string' },
        },
        required: ['title', 'file'],
      },
    },
  },
  required: ['findings'],
}

const VERDICT = {
  type: 'object',
  properties: {
    refuted: { type: 'boolean' },
    reason: { type: 'string' },
  },
  required: ['refuted'],
}

phase('Scope')
const scope = await agent(
  `Run \`git diff ${base}...HEAD --stat\` and \`git log ${base}..HEAD --oneline\` in this repository. ` +
  `Reply with a plain list of the changed file paths, one per line, and nothing else.`,
  { label: 'scope the diff', phase: 'Scope', effort: 'low' })

if (!scope) {
  log('could not read the diff — is this a git repository with a ' + base + ' branch?')
  return { confirmed: [], error: 'diff unavailable' }
}
log('diff scoped')

// pipeline, not parallel-then-parallel: a dimension's findings start being
// refuted the moment that dimension finishes, instead of waiting for the
// slowest reviewer to catch up.
const reviewed = await pipeline(
  DIMENSIONS,
  dimension => agent(
    `You are reviewing a git branch. Run \`git diff ${base}...HEAD\` to see the change.\n\n` +
    `Changed files:\n${scope}\n\n` +
    `Report ONLY findings about: ${dimension}\n` +
    `Read the surrounding code before claiming anything — a finding must name a file and describe ` +
    `a concrete way the code produces a wrong result. Report nothing rather than something speculative.`,
    { label: 'review: ' + dimension.split('—')[0].trim(), phase: 'Review', schema: FINDINGS }),

  review => parallel(((review && review.findings) || []).map(finding => () =>
    agent(
      `A reviewer claims the following about this repository:\n${JSON.stringify(finding, null, 2)}\n\n` +
      `Your job is to REFUTE it. Read the actual code at ${finding.file} and everything it calls. ` +
      `Set refuted=true if the claim is wrong, already handled elsewhere, or cannot actually happen. ` +
      `Default to refuted=true when you are uncertain: a false alarm costs more than a missed nit.`,
      { label: 'refute: ' + finding.title, phase: 'Verify', schema: VERDICT })
      .then(verdict => ({ ...finding, verdict })))),
)

const all = reviewed.flat().filter(Boolean)
const confirmed = all.filter(f => f.verdict && f.verdict.refuted === false)
log(`${all.length} findings raised, ${confirmed.length} survived refutation`)

return { base, confirmed, refuted: all.length - confirmed.length }
