export type ProjectStatus = 'active' | 'on_hold' | 'completed' | 'archived';
export type TaskStatus    = 'todo' | 'in_progress' | 'done' | 'blocked';
export type GoalStatus    = 'active' | 'completed' | 'archived';

export interface Project {
  id: string;
  goal_id?: string | null;
  name: string;
  title: string;
  description?: string | null;
  status: ProjectStatus;
  area: string;
  priority: 1 | 2 | 3 | 4 | 5;
  created_at: string;
  updated_at: string;
}

export interface Task {
  id: string;
  project_id?: string | null;
  title: string;
  description?: string | null;
  status: TaskStatus;
  priority: 1 | 2 | 3 | 4 | 5;
  assignee?: string | null;
  due_date?: string | null;
  artifact?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Goal {
  id: string;
  title: string;
  description?: string | null;
  status: GoalStatus;
  area?: string | null;
  due_date?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Repo {
  id: string;
  name: string;
  path?: string | null;
  description?: string | null;
  language?: string | null;
  status: string;
  current_branch?: string | null;
  known_issues: string[];
  next_planned_step?: string | null;
  last_activity?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Decision {
  id: string;
  project_id?: string | null;
  repo_name?: string | null;
  title: string;
  context: string;
  decision: string;
  rationale: string;
  alternatives?: string | null;
  created_at: string;
}

export interface SessionHandoff {
  id: string;
  project_id?: string | null;
  repo_name?: string | null;
  intent: string;
  context_summary?: string | null;
  resolved_at?: string | null;
  created_at: string;
}

export interface WeeklyProgress {
  completed: number;
  total: number;
}

export interface TodayContext {
  goals: Goal[];
  projects: Project[];
  weekly_progress: WeeklyProgress;
  pending_handoff: SessionHandoff | null;
}

export interface CreateTaskRequest {
  title: string;
  project_id?: string | null;
  priority: 1 | 2 | 3 | 4 | 5;
  due_date?: string | null;
}
