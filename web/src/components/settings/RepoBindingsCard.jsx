import { Card, CardContent } from '../ui/card.jsx';

export default function RepoBindingsCard({ bindings, bindingsError, bindingsLoading }) {
  return (
    <Card className="border-border/70">
      <CardContent className="space-y-4 pt-6">
        <div className="space-y-2">
          <h3>GitHub bindings</h3>
          <p className="status">Tracked remotes bound into your home slice for import, pull, and optional pushback.</p>
        </div>
        {bindingsLoading && <div className="panel-empty">Loading bindings...</div>}
        {!bindingsLoading && bindingsError && <div className="panel-error">{bindingsError}</div>}
        {!bindingsLoading && !bindingsError && bindings.length === 0 && (
          <div className="panel-empty" data-testid="settings-empty-state">
            No GitHub bindings yet. Use <code>gs repo import &lt;repo-url&gt; &lt;/absolute/path&gt;</code> to add one.
          </div>
        )}
        {!bindingsLoading && !bindingsError && bindings.length > 0 && (
          <div className="space-y-3" data-testid="settings-repo-bindings">
            {bindings.map((binding) => (
              <div key={binding.binding_id || binding.path} className="rounded-lg border border-border/70 bg-background/40 p-3">
                <div className="kv">
                  <div className="kv-row">
                    <span className="kv-key">Path</span>
                    <span className="kv-val">{binding.path}</span>
                  </div>
                  <div className="kv-row">
                    <span className="kv-key">Repo</span>
                    <span className="kv-val">{binding.repo_url}</span>
                  </div>
                  <div className="kv-row">
                    <span className="kv-key">Branch</span>
                    <span className="kv-val">{binding.branch || 'default'}</span>
                  </div>
                  <div className="kv-row">
                    <span className="kv-key">Push enabled</span>
                    <span className="kv-val">{binding.push_enabled ? 'yes' : 'no'}</span>
                  </div>
                  <div className="kv-row">
                    <span className="kv-key">Last imported</span>
                    <span className="kv-val">{binding.last_imported_commit || 'not imported yet'}</span>
                  </div>
                  <div className="kv-row">
                    <span className="kv-key">Last pushed</span>
                    <span className="kv-val">{binding.last_pushed_commit || 'never'}</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
