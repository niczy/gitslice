import { formatAuthMethodType } from './AccountSettingsHelpers.js';
import { Badge } from '../ui/badge.jsx';
import { Button } from '../ui/button.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export default function AuthMethodsCard({
  authMethods,
  authMethodsError,
  authMethodsLoading,
  onDeleteAuthMethod,
  removingMethodId,
}) {
  return (
    <Card className="border-border/70">
      <CardContent className="space-y-4 pt-6">
        <div className="space-y-2">
          <h3>Linked sign-in methods</h3>
          <p className="status">
            Provider-backed human sign-in methods and local password fallbacks appear here when they exist.
          </p>
        </div>
        {authMethodsLoading && <div className="panel-empty">Loading auth methods...</div>}
        {!authMethodsLoading && authMethodsError && <div className="panel-error">{authMethodsError}</div>}
        {!authMethodsLoading && !authMethodsError && authMethods.length === 0 && (
          <div className="panel-empty" data-testid="settings-auth-methods-empty">
            No linked human sign-in methods yet.
          </div>
        )}
        {!authMethodsLoading && !authMethodsError && authMethods.length > 0 && (
          <div className="space-y-3" data-testid="settings-auth-methods">
            {authMethods.map((method) => (
              <div key={method.id} className="rounded-lg border border-border/70 bg-background/40 p-3">
                <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                  <div className="kv flex-1">
                    <div className="kv-row">
                      <span className="kv-key">Provider</span>
                      <span className="kv-val">{method.provider || formatAuthMethodType(method.type)}</span>
                    </div>
                    <div className="kv-row">
                      <span className="kv-key">Type</span>
                      <span className="kv-val">{formatAuthMethodType(method.type)}</span>
                    </div>
                    <div className="kv-row">
                      <span className="kv-key">Email</span>
                      <span className="kv-val">{method.email || 'not set'}</span>
                    </div>
                    <div className="kv-row">
                      <span className="kv-key">Linked</span>
                      <span className="kv-val">{method.linked_at || method.linkedAt || 'unknown'}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <Badge variant="outline">{method.provider || formatAuthMethodType(method.type)}</Badge>
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      disabled={removingMethodId === method.id}
                      onClick={() => onDeleteAuthMethod(method.id)}
                      data-testid={`settings-auth-method-delete-${method.id}`}
                    >
                      {removingMethodId === method.id ? 'Removing...' : 'Remove'}
                    </Button>
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
