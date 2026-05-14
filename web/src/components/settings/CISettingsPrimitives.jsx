export function Metric({ label, value }) {
  return (
    <div className="rounded-md border border-border/70 bg-background/50 p-3">
      <div className="text-xs font-medium uppercase tracking-normal text-muted-foreground">{label}</div>
      <div className="mt-1 font-mono text-xl font-semibold text-foreground">{value}</div>
    </div>
  );
}

export function DataError({ children }) {
  if (!children) return null;
  return <div className="panel-error">{children}</div>;
}
