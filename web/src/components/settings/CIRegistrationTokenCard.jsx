import { Clipboard } from 'lucide-react';

import { formatDateTime } from './CISettingsHelpers.js';
import { DataError } from './CISettingsPrimitives.jsx';
import { Badge } from '../ui/badge.jsx';
import { Button } from '../ui/button.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export default function CIRegistrationTokenCard({
  copyStatus,
  errors,
  generatedToken,
  onCopyTokenCommand,
  onTokenCreate,
  onTokenLabelsChange,
  onTokenNameChange,
  onTokenPoolChange,
  onTokenTTLChange,
  tokenCommand,
  tokenLabels,
  tokenName,
  tokenPool,
  tokenTTL,
}) {
  return (
    <Card className="border-border/70">
      <CardContent className="space-y-4 pt-6">
        <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
          <div>
            <h4 className="text-base font-semibold">Registration token</h4>
            <p className="status">Tokens are short-lived and shown once. Register from the VM that will run jobs.</p>
          </div>
          <Badge variant="outline">Docker preferred</Badge>
        </div>
        <form className="grid gap-3 md:grid-cols-[1fr_1fr_1fr_120px_auto]" onSubmit={onTokenCreate}>
          <label className="space-y-2">
            <span className="text-sm font-medium">Runner name</span>
            <input className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm" value={tokenName} onChange={(event) => onTokenNameChange(event.target.value)} />
          </label>
          <label className="space-y-2">
            <span className="text-sm font-medium">Pool</span>
            <input className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm" value={tokenPool} onChange={(event) => onTokenPoolChange(event.target.value)} />
          </label>
          <label className="space-y-2">
            <span className="text-sm font-medium">Labels</span>
            <input className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm" value={tokenLabels} onChange={(event) => onTokenLabelsChange(event.target.value)} />
          </label>
          <label className="space-y-2">
            <span className="text-sm font-medium">TTL</span>
            <input className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm" value={tokenTTL} onChange={(event) => onTokenTTLChange(event.target.value)} />
          </label>
          <div className="flex items-end">
            <Button type="submit">Create</Button>
          </div>
        </form>
        <DataError>{errors.token}</DataError>
        {generatedToken?.token && (
          <div className="rounded-md border border-border/70 bg-muted/40 p-3" data-testid="settings-ci-token">
            <div className="mb-2 flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
              <div className="text-sm font-medium">Token expires {formatDateTime(generatedToken.expires_at || generatedToken.expiresAt)}</div>
              <Button type="button" size="sm" variant="outline" onClick={onCopyTokenCommand}>
                <Clipboard className="h-4 w-4" />
                {copyStatus || 'Copy commands'}
              </Button>
            </div>
            <pre className="overflow-auto rounded border border-border/60 bg-background p-3 font-mono text-xs">{tokenCommand}</pre>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
