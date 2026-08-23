export const meta = {
  name: 'explain-subsystem',
  description: 'Explain how a subsystem works by reading it from several angles at once, then synthesising one account',
  whenToUse: 'landing in unfamiliar code, or writing docs for something you did not build',
  phases: [
    { title: 'Sweep', detail: 'one agent per search angle' },
    { title: 'Synthesise', detail: 'a single account from all of them' },
    { title: 'Critique', detail: 'what is still missing' },
  ],
}

// Args: { subject: "the LSP integration" } — or a bare string.
const subject = typeof args === 'string' ? args : (args && args.subject)
if (!subject) {
  throw new Error('explain-subsystem needs a subject: {"subject": "the LSP integration"}')
}

// Each angle is blind to what the others surface. That is the point: one
// search strategy reliably misses whole categories of relevant code.
const ANGLES = [
  `Find the entry points for ${subject}: what calls into it from outside, and what public API does it expose?`,
  `Find the data structures behind ${subject}: the types it owns, what state they hold, and who mutates them.`,
  `Find the failure paths in ${subject}: what can go wrong, what it does about it, and what it deliberately ignores.`,
  `Find the tests for ${subject}: what behaviour they pin down, and what they conspicuously do not cover.`,
  `Find the documentation and comments about ${subject}, including any that look out of date relative to the code.`,
]

phase('Sweep')
const notes = (await parallel(ANGLES.map((angle, i) => () =>
  agent(
    `${angle}\n\nSearch this repository and read the files you find. ` +
    `Reply with concrete findings: file paths, symbol names, and what they do. ` +
    `Say "nothing found" rather than guessing.`,
    { label: `angle ${i + 1}`, phase: 'Sweep' })))).filter(Boolean)

if (notes.length === 0) {
  return { subject, summary: 'no agent could find anything about ' + subject }
}
log(`${notes.length}/${ANGLES.length} angles returned findings`)

phase('Synthesise')
const summary = await agent(
  `Five researchers independently investigated "${subject}" in this repository. ` +
  `Their notes follow, separated by markers.\n\n` +
  notes.map((n, i) => `=== researcher ${i + 1} ===\n${n}`).join('\n\n') +
  `\n\nWrite one coherent account of how ${subject} works: the entry points, the flow of control, ` +
  `the state involved, and the failure handling. Where the notes disagree, read the code yourself ` +
  `and say which is right. Cite file paths. Do not pad it — length is not the goal.`,
  { label: 'synthesise', phase: 'Synthesise', effort: 'high' })

phase('Critique')
const gaps = await agent(
  `Here is an account of "${subject}" in this repository:\n\n${summary}\n\n` +
  `What is missing or wrong? Verify its claims against the code and list: claims that do not hold, ` +
  `parts of the subsystem it does not mention, and questions it leaves unanswered. ` +
  `Reply "nothing significant" only if you genuinely find nothing.`,
  { label: 'critique', phase: 'Critique' })

return { subject, summary, gaps }
