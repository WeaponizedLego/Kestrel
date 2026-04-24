// Subsequence-based fuzzy matcher. Returns null when `query` is not a
// subsequence of `target` (case-insensitive); otherwise a positive score
// where higher is better. Bonuses favor start-anchored matches, matches
// at word boundaries, and contiguous runs, which lines up with what users
// expect when typing a tag fragment.

const BOUNDARY = /[\s\-_./]/

export function fuzzyScore(query: string, target: string): number | null {
  const q = query.toLowerCase()
  const t = target.toLowerCase()
  if (q === '') return 0
  let ti = 0
  let qi = 0
  let score = 0
  let run = 0
  let prevMatch = -2
  while (ti < t.length && qi < q.length) {
    if (t[ti] === q[qi]) {
      let bonus = 1
      if (ti === 0) bonus += 4
      else if (BOUNDARY.test(t[ti - 1])) bonus += 2
      if (ti === prevMatch + 1) {
        run += 1
        bonus += run
      } else {
        run = 0
      }
      score += bonus
      prevMatch = ti
      qi += 1
    }
    ti += 1
  }
  if (qi < q.length) return null
  return score - t.length * 0.01
}
