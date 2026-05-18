import { LayoutDashboard, ListTodo, FolderGit2, BookMarked, Library, GraduationCap, History, Calendar, ClipboardList, Eye } from 'lucide-react'
import { NavItem } from './NavItem'

interface SidebarProps {
  collapsed?: boolean;
  onNavigate?: () => void;
}

const navItems = [
  { icon: LayoutDashboard, labelKey: 'nav.dashboard',        to: '/',                 phase: 1 as const },
  { icon: ListTodo,        labelKey: 'nav.gtd',              to: '/gtd',              phase: 1 as const },
  { icon: FolderGit2,      labelKey: 'nav.workspace',        to: '/workspace',        phase: 1 as const },
  { icon: BookMarked,      labelKey: 'nav.decisions',        to: '/decisions',        phase: 1 as const },
  { icon: Library,         labelKey: 'nav.knowledge',        to: '/knowledge',        phase: 1 as const },
  { icon: GraduationCap,   labelKey: 'nav.reviews',          to: '/reviews',          phase: 1 as const },
  { icon: History,         labelKey: 'nav.learningHistory',  to: '/learning/history', phase: 1 as const },
  { icon: Calendar,        labelKey: 'nav.calendar',         to: '/calendar',         phase: 1 as const },
  { icon: ClipboardList,   labelKey: 'nav.proposals',        to: '/proposals',        phase: 1 as const },
  { icon: Eye,             labelKey: 'nav.vision',           to: '/vision',           phase: 1 as const },
]

export function Sidebar({ collapsed = false, onNavigate }: SidebarProps) {
  return (
    <nav
      className="flex flex-col py-4 gap-1 h-full"
      style={{
        background: 'var(--color-bg-card)',
        borderRight: '1px solid var(--color-border)',
        width: collapsed ? '56px' : '240px',
        overflowX: 'hidden',
      }}
      aria-label="Main navigation"
    >
      {navItems.map((item) => (
        <NavItem
          key={item.to}
          icon={item.icon}
          labelKey={item.labelKey}
          to={item.to}
          phase={item.phase}
          collapsed={collapsed}
          onNavigate={onNavigate}
        />
      ))}
    </nav>
  )
}
