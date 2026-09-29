// [F0929-52] The Projects tab bar must expose ARIA tablist semantics
// (role="tablist"/"tab"/"tabpanel", aria-selected, aria-controls,
// aria-labelledby) matching the working VisionPage.tsx pattern, so
// screen-reader users can identify the row as a tab group and perceive the
// current selection.
import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { ProjectList } from './ProjectList'
import type { Project } from '../../types/api'

function makeProject(overrides: Partial<Project>): Project {
  return {
    id: overrides.id ?? 'p1',
    name: overrides.name ?? 'proj-1',
    title: overrides.title ?? 'Project One',
    status: overrides.status ?? 'active',
    area: overrides.area ?? 'Engineering',
    priority: overrides.priority ?? 3,
    created_at: '2026-05-01T00:00:00Z',
    updated_at: '2026-05-01T00:00:00Z',
    ...overrides,
  }
}

// No on_hold project here on purpose — the empty-state test below relies on
// the "On Hold" tab's filtered list being empty.
const projects: Project[] = [
  makeProject({ id: 'p1', title: 'Active project', status: 'active' }),
  makeProject({ id: 'p2', title: 'Completed project', status: 'completed' }),
]

function renderList(items: Project[] = projects) {
  return render(
    <MemoryRouter>
      <ProjectList projects={items} />
    </MemoryRouter>,
  )
}

describe('ProjectList tab bar ARIA semantics', () => {
  it('exposes tablist semantics with the All tab selected', () => {
    renderList()

    const tablist = screen.getByRole('tablist')
    expect(tablist).toHaveAttribute('aria-label', 'Project status filter')

    const tabs = screen.getAllByRole('tab')
    expect(tabs).toHaveLength(4)
    for (const tab of tabs) {
      expect(tab).toHaveAttribute('aria-controls', 'gtd-project-tabpanel')
    }

    const allTab = screen.getByRole('tab', { name: 'All' })
    expect(allTab).toHaveAttribute('aria-selected', 'true')

    const activeTab = screen.getByRole('tab', { name: 'Active' })
    expect(activeTab).toHaveAttribute('aria-selected', 'false')
  })

  it('moves aria-selected and aria-labelledby when a tab is clicked', async () => {
    const user = userEvent.setup()
    renderList()

    await user.click(screen.getByRole('tab', { name: 'Active' }))

    expect(screen.getByRole('tab', { name: 'Active' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'All' })).toHaveAttribute('aria-selected', 'false')
    expect(screen.getByRole('tabpanel')).toHaveAttribute('aria-labelledby', 'gtd-project-tab-active')
  })

  it('keeps the empty state inside the tabpanel', async () => {
    const user = userEvent.setup()
    renderList()

    await user.click(screen.getByRole('tab', { name: 'On Hold' }))

    const tabpanel = screen.getByRole('tabpanel')
    expect(tabpanel).toHaveAttribute('aria-labelledby', 'gtd-project-tab-on_hold')
    expect(within(tabpanel).getByText('No projects')).toBeInTheDocument()
  })
})
