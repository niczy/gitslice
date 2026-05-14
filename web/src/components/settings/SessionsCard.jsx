import { Badge } from '../ui/badge.jsx';
import { Button } from '../ui/button.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export default function SessionsCard({
  onSessionRevoke,
  revokingSessionId,
  sessions,
  sessionsError,
  sessionsLoading,
}) {
  return (
    <Card className="border-border/70">
      <CardContent className="space-y-4 pt-6">
        <div className="space-y-2">
          <h3>Gitslice sessions</h3>
          <p className="status">
            Web, CLI, device, and agent sessions use local Gitslice API credentials here.
          </p>
        </div>
        {sessionsLoading && <div className="panel-empty">Loading sessions...</div>}
        {!sessionsLoading && sessionsError && <div className="panel-error">{sessionsError}</div>}
        {!sessionsLoading && !sessionsError && sessions.length === 0 && (
          <div className="panel-empty" data-testid="settings-sessions-empty">
            No Gitslice sessions yet.
          </div>
        )}
        {!sessionsLoading && !sessionsError && sessions.length > 0 && (
          <div className="space-y-3" data-testid="settings-sessions">
            {sessions.map((session) => (
              <div key={session.id} className="rounded-lg border border-border/70 bg-background/40 p-3">
                <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                  <div className="kv flex-1">
                    <div className="kv-row">
                      <span className="kv-key">Session ID</span>
                      <span className="kv-val break-all">{session.id}</span>
                    </div>
                    <div className="kv-row">
                      <span className="kv-key">Device</span>
                      <span className="kv-val">{session.device_info || session.deviceInfo || 'unknown'}</span>
                    </div>
                    <div className="kv-row">
                      <span className="kv-key">Last seen</span>
                      <span className="kv-val">{session.last_seen_at || session.lastSeenAt || 'unknown'}</span>
                    </div>
                    <div className="kv-row">
                      <span className="kv-key">Agent key</span>
                      <span className="kv-val">{session.agent_key_id || session.agentKeyId || 'none'}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <Badge variant={session.current ? 'secondary' : 'outline'}>
                      {session.current ? 'Current' : 'Active'}
                    </Badge>
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      disabled={revokingSessionId === session.id}
                      onClick={() => onSessionRevoke(session.id, Boolean(session.current))}
                      data-testid={`settings-session-revoke-${session.id}`}
                    >
                      {revokingSessionId === session.id
                        ? 'Revoking...'
                        : session.current
                          ? 'Revoke and sign out'
                          : 'Revoke'}
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
