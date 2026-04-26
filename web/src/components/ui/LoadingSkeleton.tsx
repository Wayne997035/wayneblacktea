interface LoadingSkeletonProps {
  className?: string;
  lines?: number;
  style?: React.CSSProperties;
}

export function LoadingSkeleton({ className = '', lines = 1, style }: LoadingSkeletonProps) {
  return (
    <div role="status" aria-label="Loading...">
      {Array.from({ length: lines }, (_, i) => (
        <div
          key={i}
          className={`skeleton rounded-md ${i > 0 ? 'mt-2' : ''} ${className}`}
          style={{ height: '1rem', ...style }}
        />
      ))}
    </div>
  )
}
