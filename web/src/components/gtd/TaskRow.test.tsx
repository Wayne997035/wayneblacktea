// [F0929-53] TaskRow's PR link href must go through the shared safeHref()
// scheme allowlist (http/https/wbt, control-char/leading-whitespace
// rejection) instead of a hand-rolled `startsWith('https://')` guard, so it
// gets the same XSS hardening as KnowledgeCard.tsx's link.
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TaskRow } from './TaskRow'
import type { Task } from '../../types/api'

function makeTask(overrides: Partial<Task>): Task {
  return {
    id: 't1',
    project_id: null,
    title: 'Ship the thing',
    status: 'in_progress',
    area: 'Engineering',
    priority: 3,
    created_at: '2026-05-01T00:00:00Z',
    updated_at: '2026-05-01T00:00:00Z',
    ...overrides,
  }
}

function renderRow(pr_url: string) {
  return render(
    <ul>
      <TaskRow task={makeTask({ pr_url })} expanded onToggle={() => {}} />
    </ul>,
  )
}

describe('TaskRow PR link safety', () => {
  it('renders https PR links as-is', () => {
    renderRow('https://github.com/org/repo/pull/1')
    expect(screen.getByRole('link', { name: 'https://github.com/org/repo/pull/1' })).toHaveAttribute(
      'href',
      'https://github.com/org/repo/pull/1',
    )
  })

  it('neutralises javascript: PR links to #', () => {
    renderRow('javascript:alert(1)')
    expect(screen.getByRole('link', { name: 'javascript:alert(1)' })).toHaveAttribute('href', '#')
  })

  it('allows wbt: PR links via safeHref', () => {
    renderRow('wbt://internal-artifact/1')
    expect(screen.getByRole('link', { name: 'wbt://internal-artifact/1' })).toHaveAttribute(
      'href',
      'wbt://internal-artifact/1',
    )
  })
})
