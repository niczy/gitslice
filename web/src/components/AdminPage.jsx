import { Badge } from './ui/badge.jsx';
import { Card, CardContent } from './ui/card.jsx';

export default function AdminPage() {
  return (
    <section className="section space-y-4" data-testid="admin-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Administration</Badge>
        <h2>Admin Console</h2>
        <p>Administrative operations are available for privileged accounts.</p>
      </div>
      <Card className="border-border/70">
        <CardContent className="pt-6">
          <div className="panel-empty">No admin actions are configured for this deployment yet.</div>
        </CardContent>
      </Card>
    </section>
  );
}
