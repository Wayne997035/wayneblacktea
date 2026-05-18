import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { TaskRow } from './TaskRow'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { useTasksByProject, useAllTasks, useCompleteTask } from '../../hooks/useTasks'
import type { Project } from '../../types/api'

interface TaskListProps {
  projects: Project[];
  linkedTaskId?: string;
}

interface SingleProjectTasksProps {
  projectId: string;
  projects: Project[];
}

function SingleProjectTasks({ projectId, projects }: SingleProjectTasksProps) {
  const { data: tasks = [], isLoading } = useTasksByProject(projectId)
  const [expandedTaskId, setExpandedTaskId] = useState<string | null>(null)
  const completeTask = useCompleteTask(projectId)

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
            onComplete={(taskId) => { void completeTask.mutateAsync(taskId) }}
          />
        )
      })}
    </ul>
  )
}

interface AllProjectsTasksProps {
  projects: Project[];
  linkedTaskId?: string;
}

function AllProjectsTasks({ projects, linkedTaskId }: AllProjectsTasksProps) {
  const { data: tasks = [], isLoading } = useAllTasks()
  const [expandedTaskId, setExpandedTaskId] = useState<string | null>(null)
  const completeTask = useCompleteTask()
  const didAutoExpand = useRef(false)

  useEffect(() => {
    if (!linkedTaskId || isLoading || didAutoExpand.current) return
    const found = tasks.find((t) => t.id === linkedTaskId)
    if (!found) return
    didAutoExpand.current = true
    setExpandedTaskId(linkedTaskId)
    // Scroll the task row into view after React renders
    requestAnimationFrame(() => {
      const el = document.getElementById(`task-detail-${linkedTaskId}`)?.closest('li')
      el?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    })
  }, [linkedTaskId, tasks, isLoading])

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
            onComplete={(taskId) => { void completeTask.mutateAsync(taskId) }}
          />
        )
      })}
    </ul>
  )
}

export function TaskList({ projects, linkedTaskId }: TaskListProps) {
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
        <AllProjectsTasks projects={projects} linkedTaskId={linkedTaskId} />
      ) : (
        <SingleProjectTasks projectId={selectedProjectId} projects={projects} />
      )}
    </div>
  )
}
