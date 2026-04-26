import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ProjectCard } from '../dashboard/ProjectCard'
import { EmptyState } from '../ui/EmptyState'
import type { Project, ProjectStatus } from '../../types/api'

interface ProjectListProps {
  projects: Project[];
}

type TabFilter = 'all' | ProjectStatus

const tabs: { key: TabFilter; labelKey: string }[] = [
  { key: 'all',       labelKey: 'gtd.tabs.all' },
  { key: 'active',    labelKey: 'gtd.tabs.active' },
  { key: 'on_hold',   labelKey: 'gtd.tabs.on_hold' },
  { key: 'completed', labelKey: 'gtd.tabs.completed' },
]

export function ProjectList({ projects }: ProjectListProps) {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState<TabFilter>('all')

  const filtered = activeTab === 'all'
    ? projects
    : projects.filter((p) => p.status === activeTab)

  return (
    <div>
      {/* Tab bar */}
      <div className="flex gap-1 mb-4 border-b" style={{ borderColor: 'var(--color-border)' }}>
        {tabs.map(({ key, labelKey }) => (
          <button
            key={key}
            type="button"
            onClick={() => setActiveTab(key)}
            className="px-4 py-2 text-body transition-colors"
            style={{
              color: activeTab === key ? 'var(--color-text-primary)' : 'var(--color-text-muted)',
              borderBottom: activeTab === key ? '2px solid var(--color-accent-blue)' : '2px solid transparent',
              background: 'transparent',
              marginBottom: '-1px',
            }}
          >
            {t(labelKey)}
          </button>
        ))}
      </div>

      {filtered.length === 0 ? (
        <EmptyState messageKey="gtd.noProjects" />
      ) : (
        <div className="flex flex-col gap-3">
          {filtered.map((project) => (
            <ProjectCard key={project.id} project={project} variant="expanded" />
          ))}
        </div>
      )}
    </div>
  )
}
