import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { TaskRow } from './TaskRow'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { useTasksByProject, useTasksForAllProjects } from '../../hooks/useTasks'
import type { Project } from '../../types/api'

interface TaskListProps {
  projects: Project[];
}

interface SingleProjectTasksProps {
  projectId: string;
  projects: Project[];
}

function SingleProjectTasks({ projectId, projects }: SingleProjectTasksProps) {
  const { data: tasks = [], isLoading } = useTasksByProject(projectId)
  const [expandedTaskId, setExpandedTaskId] = useState<string | null>(null)

  if (isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 5 }, (_, i) => (
          <LoadingSkeleton key={i} className="h-11 w-full" />
        ))}
      </div>
    )
  }
  if (tasks.length === 0) {
    return <EmptyState messageKey="gtd.noTasks" />
  }
  return (
    <ul aria-label="Task list" className="rounded-lg overflow-hidden" style={{ border: '1px solid var(--color-border)' }}>
      {tasks.map((task) => {
        const project = task.project_id ? projects.find((p) => p.id === task.project_id) : undefined
        return (
          <TaskRow
            key={task.id}
            task={task}
            project={project}
            expanded={expandedTaskId === task.id}
            onToggle={() => setExpandedTaskId((prev) => (prev === task.id ? null : task.id))}
          />
        )
      })}
    </ul>
  )
}

interface AllProjectsTasksProps {
  projects: Project[];
}

function AllProjectsTasks({ projects }: AllProjectsTasksProps) {
  const activeProjects = projects.filter((p) => p.status === 'active')
  const { isLoading, tasks } = useTasksForAllProjects(activeProjects)
  const [expandedTaskId, setExpandedTaskId] = useState<string | null>(null)

  if (isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 5 }, (_, i) => (
          <LoadingSkeleton key={i} className="h-11 w-full" />
        ))}
      </div>
    )
  }
  if (tasks.length === 0) {
    return <EmptyState messageKey="gtd.noTasks" />
  }
  return (
    <ul aria-label="Task list" className="rounded-lg overflow-hidden" style={{ border: '1px solid var(--color-border)' }}>
      {tasks.map((task) => {
        const project = task.project_id ? projects.find((p) => p.id === task.project_id) : undefined
        return (
          <TaskRow
            key={task.id}
            task={task}
            project={project}
            expanded={expandedTaskId === task.id}
            onToggle={() => setExpandedTaskId((prev) => (prev === task.id ? null : task.id))}
          />
        )
      })}
    </ul>
  )
}

export function TaskList({ projects }: TaskListProps) {
  const { t } = useTranslation()
  const [selectedProjectId, setSelectedProjectId] = useState<string>('all')

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

      {selectedProjectId === 'all' ? (
        <AllProjectsTasks projects={projects} />
      ) : (
        <SingleProjectTasks projectId={selectedProjectId} projects={projects} />
      )}
    </div>
  )
}
