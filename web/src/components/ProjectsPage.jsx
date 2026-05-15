import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';

export default function ProjectsPage({ slices, slicesLoading, slicesError, onOpenRepos, onRefresh }) {
  const rootSlices = slices.filter((slice) => slice.is_root);
  const projectSlices = slices.filter((slice) => !slice.is_root);
  return (
    <section className="section space-y-4" data-testid="projects-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Workspace</Badge>
        <h2>Projects</h2>
        <p>Track the slice projects currently available in this environment and jump back into active work quickly.</p>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <Card className="border-border/70">
          <CardContent className="pt-6">
            <div className="status">Total slices</div>
            <div className="text-3xl font-semibold">{slices.length}</div>
          </CardContent>
        </Card>
        <Card className="border-border/70">
          <CardContent className="pt-6">
            <div className="status">Shared roots</div>
            <div className="text-3xl font-semibold">{rootSlices.length}</div>
          </CardContent>
        </Card>
        <Card className="border-border/70">
          <CardContent className="pt-6">
            <div className="status">Active slices</div>
            <div className="text-3xl font-semibold">{projectSlices.length}</div>
          </CardContent>
        </Card>
      </div>

      <Card className="border-border/70">
        <CardContent className="space-y-4 pt-6">
          {slicesLoading && <div className="status">Loading projects…</div>}
          {!slicesLoading && slicesError && <div className="panel-error">{slicesError}</div>}
          {!slicesLoading && !slicesError && slices.length === 0 && (
            <div className="panel-empty" data-testid="projects-empty-state">
              No projects were found yet. Open Repos to initialize or sync repository slices.
            </div>
          )}

          {!slicesLoading && !slicesError && slices.length > 0 && (
            <ul className="org-list" data-testid="projects-list">
              {slices.map((slice) => (
                <li className="org-item" key={slice.slice_id}>
                  <div className="org-item-title">
                    <span className="org-name">{slice.name || slice.slice_id}</span>
                    <span className="org-slug">{slice.slug || slice.slice_id}</span>
                  </div>
                  <div className="org-item-meta">
                    {slice.is_root ? 'Shared root slice' : 'Workspace slice'}
                    {slice.environment ? ` • env ${slice.environment}` : ''}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <div className="auth-actions">
        <Button type="button" onClick={onOpenRepos}>Open Repos</Button>
        <Button type="button" variant="ghost" onClick={onRefresh}>Refresh</Button>
      </div>
    </section>
  );
}
