/**
 * Pure date helpers shared between MonthGrid and WeekList. Keeping them
 * outside the component files lets eslint's react-refresh rule treat
 * those files as component-only modules.
 */

/**
 * buildMonthMatrix returns a 6×7 matrix of `Date` objects covering the
 * month containing `anchor`, padded with the surrounding-month days so
 * that the first cell is the Monday on/before the 1st of the month and
 * the last cell is the Sunday on/after the last day of the month. Always
 * returns 6 rows × 7 columns (42 cells) for stable layout.
 */
export function buildMonthMatrix(anchor: Date): Date[][] {
  const year = anchor.getFullYear()
  const month = anchor.getMonth()
  const first = new Date(year, month, 1)
  // JS getDay(): 0=Sun..6=Sat; we want Mon=0..Sun=6
  const isoWeekday = (first.getDay() + 6) % 7
  const start = new Date(year, month, 1 - isoWeekday)

  const rows: Date[][] = []
  for (let r = 0; r < 6; r++) {
    const row: Date[] = []
    for (let c = 0; c < 7; c++) {
      const d = new Date(start.getFullYear(), start.getMonth(), start.getDate() + r * 7 + c)
      row.push(d)
    }
    rows.push(row)
  }
  return rows
}

/** mondayOf returns the Monday of the ISO week containing `d`. */
export function mondayOf(d: Date): Date {
  const isoWeekday = (d.getDay() + 6) % 7
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() - isoWeekday)
}

/** dateKey returns "YYYY-MM-DD" in the local timezone. */
export function dateKey(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

/** isSameDay compares only year/month/day in the local timezone. */
export function isSameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  )
}
