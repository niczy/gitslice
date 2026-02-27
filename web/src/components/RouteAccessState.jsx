import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';

export default function RouteAccessState({ state, onGoToLogin }) {
  if (state === 'unauthorized') {
    return (
      <section className="section" data-testid="route-access-denied">
        <div className="section-header">
          <Badge variant="secondary" className="eyebrow">Restricted</Badge>
          <h2>Access denied</h2>
          <p>Your account does not have permission to view this route.</p>
        </div>
      </section>
    );
  }

  return (
    <section className="section space-y-4" data-testid="route-auth-required">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Authentication</Badge>
        <h2>Please sign in</h2>
        <p>You need to log in before viewing this route.</p>
      </div>
      <Card className="border-border/70">
        <CardContent className="pt-6">
          <div className="auth-actions">
            <Button type="button" onClick={onGoToLogin}>
              Go to login
            </Button>
          </div>
        </CardContent>
      </Card>
    </section>
  );
}
