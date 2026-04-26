import { DecisionEntry } from './DecisionEntry'
import type { Decision } from '../../types/api'

interface DecisionTimelineProps {
  decisions: Decision[];
}

export function DecisionTimeline({ decisions }: DecisionTimelineProps) {
  return (
    <div
      className="relative border-l-2 ml-4 pl-4 space-y-6"
      style={{ borderColor: 'var(--color-border)' }}
    >
      {decisions.map((d) => (
        <DecisionEntry key={d.id} decision={d} />
      ))}
    </div>
  )
}
