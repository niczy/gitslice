import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';

export default function ProjectsPage({ slices, slicesLoading, slicesError, onOpenRepos }) {
  return (
    <section className="section space-y-4" data-testid="projects-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Workspace</Badge>
        <h2>Projects</h2>
        <p>Track the slice projects currently available in this environment.</p>
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
                    <span className="org-slug">{slice.slice_id}</span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <div className="auth-actions">
        <Button type="button" onClick={onOpenRepos}>Open Repos</Button>
      </div>
    </section>
  );
}
