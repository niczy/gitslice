import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';

export default function NotFoundPage({ unknownPath, onGoHome }) {
  return (
    <section className="section space-y-4" data-testid="not-found-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Navigation</Badge>
        <h2>Page not found</h2>
        <p>No page exists for <code>/{unknownPath || ''}</code>.</p>
      </div>
      <Card className="border-border/70">
        <CardContent className="space-y-4 pt-6">
          <div className="panel-error">Choose a menu destination to continue.</div>
          <div className="auth-actions">
            <Button type="button" onClick={onGoHome}>Go to Get Started</Button>
          </div>
        </CardContent>
      </Card>
    </section>
  );
}
