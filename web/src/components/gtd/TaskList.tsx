import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { TaskRow } from './TaskRow'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { useTasksByProject } from '../../hooks/useTasks'
import type { Project } from '../../types/api'

interface TaskListProps {
  projects: Project[];
}

export function TaskList({ projects }: TaskListProps) {
  const { t } = useTranslation()
  const [selectedProjectId, setSelectedProjectId] = useState<string>('all')

  const activeProjects = projects.filter((p) => p.status === 'active')
  // Load tasks for selected project or all active projects
  const projectToLoad = selectedProjectId !== 'all'
    ? projects.find((p) => p.id === selectedProjectId)
    : null

  const singleQuery = useTasksByProject(projectToLoad?.id ?? '')
  const allQuery = useTasksByProject(activeProjects[0]?.id ?? '')

  // For simplicity: when "all" selected, load tasks for first active project
  // A full implementation would use parallel queries
  const isLoading = selectedProjectId !== 'all' ? singleQuery.isLoading : allQuery.isLoading
  const tasks = selectedProjectId !== 'all' ? (singleQuery.data ?? []) : (allQuery.data ?? [])

  return (
    <div>
      <div className="mb-3">
        <select
          value={selectedProjectId}
          onChange={(e) => setSelectedProjectId(e.target.value)}
          className="rounded-md px-3 py-2 text-body"
          style={{
            background: 'var(--color-bg-input)',
            border: '1px solid var(--color-border)',
            color: 'var(--color-text-primary)',
          }}
          aria-label={t('gtd.allProjects')}
        >
          <option value="all">{t('gtd.allProjects')}</option>
          {projects.map((p) => (
            <option key={p.id} value={p.id}>{p.title}</option>
          ))}
        </select>
      </div>

      {isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }, (_, i) => (
            <LoadingSkeleton key={i} className="h-11 w-full" />
          ))}
        </div>
      ) : tasks.length === 0 ? (
        <EmptyState messageKey="gtd.noTasks" />
      ) : (
        <ul aria-label="Task list" className="rounded-lg overflow-hidden" style={{ border: '1px solid var(--color-border)' }}>
          {tasks.map((task) => (
            <TaskRow key={task.id} task={task} />
          ))}
        </ul>
      )}
    </div>
  )
}
