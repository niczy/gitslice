export default function ProjectsPage({ slices, slicesLoading, slicesError, onOpenRepos }) {
  return (
    <section className="section" data-testid="projects-page">
      <div className="section-header">
        <p className="eyebrow">Workspace</p>
        <h2>Projects</h2>
        <p>Track the slice projects currently available in this environment.</p>
      </div>

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

      <div className="auth-actions" style={{ marginTop: '16px' }}>
        <button type="button" className="primary" onClick={onOpenRepos}>Open Repos</button>
      </div>
    </section>
  );
}
